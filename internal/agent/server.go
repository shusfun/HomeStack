package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/components"
	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/tailscale"
)

type TailnetStatus interface {
	Status(ctx context.Context) (tailscale.Status, error)
}

type ServerOptions struct {
	DeviceID              string
	DeviceName            string
	ConfigStore           *ConfigStore
	Sessions              *SessionStore
	Tailnet               TailnetStatus
	ModuleSecrets         map[string]map[string]string
	ManagedModuleVersions map[string]string
	System                SystemManager
	Updater               Updater
}

type Server struct {
	deviceID              string
	deviceName            string
	configStore           *ConfigStore
	sessions              *SessionStore
	tailnet               TailnetStatus
	proxyMu               sync.RWMutex
	secrets               map[string]map[string]string
	managedModuleVersions map[string]string
	files                 *FileService
	mediaProxy            http.Handler
	system                SystemManager
	updater               Updater
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.DeviceID == "" || options.DeviceName == "" || options.ConfigStore == nil || options.Sessions == nil || options.Tailnet == nil || options.Updater == nil {
		return nil, errors.New("Agent 依赖未完整配置")
	}
	_, ok := options.ConfigStore.Current()
	if !ok {
		return nil, errors.New("Agent 尚未获得有效签名配置")
	}
	server := &Server{
		deviceID: options.DeviceID, deviceName: options.DeviceName, configStore: options.ConfigStore,
		sessions: options.Sessions, tailnet: options.Tailnet, secrets: options.ModuleSecrets,
		managedModuleVersions: options.ManagedModuleVersions, system: options.System, updater: options.Updater,
	}
	if server.system == nil {
		system, err := newDefaultSystemManager()
		if err != nil {
			return nil, err
		}
		server.system = system
	}
	if err := server.Reload(); err != nil {
		return nil, err
	}
	return server, nil
}

func (s *Server) Reload() error {
	config, ok := s.configStore.Current()
	if !ok {
		return errors.New("Agent 尚未获得有效签名配置")
	}
	var mediaProxy http.Handler
	for _, module := range config.Modules {
		if !module.Enabled {
			continue
		}
		switch module.ID {
		case "jellyfin":
			apiKey := s.secrets[ModuleKey(module)]["api_key"]
			if apiKey == "" {
				return errors.New("Jellyfin 模块缺少 api_key")
			}
			proxy, err := NewJellyfinProxy(module.BaseURL, apiKey)
			if err != nil {
				return err
			}
			mediaProxy = prefixProxy("/api/media", "", proxy)
		}
	}
	var files *FileService
	if len(config.SharedDirectories) > 0 {
		files = NewFileService(config.SharedDirectories)
	}
	s.proxyMu.Lock()
	s.files = files
	s.mediaProxy = mediaProxy
	s.proxyMu.Unlock()
	return nil
}

func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /access", s.redeemTicket)
	mux.HandleFunc("GET /api/meta", func(writer http.ResponseWriter, _ *http.Request) {
		writeAgentJSON(writer, http.StatusOK, map[string]string{"surface": "agent"})
	})
	mux.HandleFunc("GET /api/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeAgentJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /api/status", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.status))))
	mux.Handle("GET /api/system/metrics", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.metrics))))
	mux.Handle("GET /api/services", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.services))))
	mux.Handle("POST /api/services/{serviceID}/actions", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.serviceAction))))
	mux.Handle("GET /api/logs", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.logs))))
	mux.Handle("GET /api/updates/status", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.updateStatus))))
	mux.Handle("POST /api/updates/check", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.checkUpdate))))
	mux.Handle("POST /api/updates/download", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.downloadUpdate))))
	mux.Handle("POST /api/updates/install", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.installUpdate))))
	mux.Handle("GET /api/files/resources", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.fileResources))))
	mux.Handle("GET /api/files/search", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.fileSearch))))
	mux.Handle("GET /api/files/raw", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.fileRaw))))
	mux.Handle("/api/media/", s.requireSession(s.requireActiveConfig(s.dynamicProxy("jellyfin"))))
	if static != nil {
		mux.Handle("/", s.requireDocumentSession(static))
	}
	return agentSecurityHeaders(mux)
}

func (s *Server) updateStatus(writer http.ResponseWriter, _ *http.Request) {
	writeAgentJSON(writer, http.StatusOK, s.updater.Status())
}

func (s *Server) checkUpdate(writer http.ResponseWriter, request *http.Request) {
	if !s.validWriteOrigin(request) {
		writeAgentError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Agent 地址不匹配")
		return
	}
	status, err := s.updater.Check(request.Context())
	if err != nil {
		writeAgentError(writer, http.StatusBadGateway, "update_check_failed", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusOK, status)
}

func (s *Server) downloadUpdate(writer http.ResponseWriter, request *http.Request) {
	if !s.validWriteOrigin(request) {
		writeAgentError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Agent 地址不匹配")
		return
	}
	status, err := s.updater.Download(request.Context())
	if err != nil {
		writeAgentError(writer, http.StatusBadGateway, "update_download_failed", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusOK, status)
}

func (s *Server) installUpdate(writer http.ResponseWriter, request *http.Request) {
	if !s.validWriteOrigin(request) {
		writeAgentError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Agent 地址不匹配")
		return
	}
	if err := s.updater.Install(); err != nil {
		writeAgentError(writer, http.StatusInternalServerError, "update_install_failed", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusAccepted, s.updater.Status())
}

func (s *Server) metrics(writer http.ResponseWriter, request *http.Request) {
	metrics, err := s.system.Metrics(request.Context())
	if err != nil {
		writeAgentError(writer, http.StatusServiceUnavailable, "metrics_failed", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusOK, metrics)
}

func (s *Server) services(writer http.ResponseWriter, request *http.Request) {
	services, err := s.system.Services(request.Context())
	if err != nil {
		writeAgentError(writer, http.StatusServiceUnavailable, "services_failed", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{"services": services})
}

func (s *Server) serviceAction(writer http.ResponseWriter, request *http.Request) {
	if !s.validWriteOrigin(request) {
		writeAgentError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Agent 地址不匹配")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 4096)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeAgentError(writer, http.StatusBadRequest, "invalid_request", "服务动作请求无效: "+err.Error())
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeAgentError(writer, http.StatusBadRequest, "invalid_request", "服务动作请求只能包含一个 JSON 对象")
		return
	}
	if err := s.system.Action(request.Context(), request.PathValue("serviceID"), body.Action); err != nil {
		writeAgentError(writer, http.StatusBadRequest, "action_rejected", err.Error())
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) logs(writer http.ResponseWriter, request *http.Request) {
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 500 {
			writeAgentError(writer, http.StatusBadRequest, "invalid_limit", "日志行数必须介于 1 和 500")
			return
		}
		limit = value
	}
	page, err := s.system.Logs(request.Context(), request.URL.Query().Get("service"), limit, request.URL.Query().Get("cursor"))
	if err != nil {
		writeAgentError(writer, http.StatusBadRequest, "logs_rejected", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusOK, page)
}

func (s *Server) requireDocumentSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		acceptsHTML := request.URL.Path == "/" || strings.Contains(request.Header.Get("Accept"), "text/html")
		if acceptsHTML {
			cookie, err := request.Cookie("homestack_session")
			if err != nil || !s.sessions.Valid(cookie.Value) {
				config, ok := s.configStore.Current()
				if !ok {
					http.Error(writer, "Agent 没有有效配置", http.StatusServiceUnavailable)
					return
				}
				target := strings.TrimRight(config.ControlURL, "/") + "/devices/" + url.PathEscape(s.deviceID) + "/open"
				http.Redirect(writer, request, target, http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) validWriteOrigin(request *http.Request) bool {
	config, ok := s.configStore.Current()
	if !ok {
		return false
	}
	origin := strings.TrimRight(request.Header.Get("Origin"), "/")
	return origin != "" && origin == strings.TrimRight(config.AgentURL, "/")
}

func (s *Server) redeemTicket(writer http.ResponseWriter, request *http.Request) {
	if !s.configActive() {
		writeAgentError(writer, http.StatusServiceUnavailable, "config_expired", "Agent 签名配置无效或已过期")
		return
	}
	ticket := request.URL.Query().Get("ticket")
	if ticket == "" {
		writeAgentError(writer, http.StatusBadRequest, "ticket_missing", "缺少访问票据")
		return
	}
	session, err := s.sessions.Redeem(ticket)
	if err != nil {
		writeAgentError(writer, http.StatusUnauthorized, "ticket_rejected", err.Error())
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name: "homestack_session", Value: session, Path: "/", MaxAge: int((8 * time.Hour).Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(writer, request, "/", http.StatusSeeOther)
}

func (s *Server) requireActiveConfig(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !s.configActive() {
			writeAgentError(writer, http.StatusServiceUnavailable, "config_expired", "Agent 签名配置无效或已过期")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) configActive() bool {
	config, ok := s.configStore.Current()
	return ok && time.Now().UTC().Before(config.ExpiresAt)
}

func (s *Server) status(writer http.ResponseWriter, request *http.Request) {
	status, err := s.BuildStatus(request.Context())
	if err != nil {
		writeAgentError(writer, http.StatusServiceUnavailable, "status_failed", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusOK, status)
}

func (s *Server) BuildStatus(ctx context.Context) (protocol.DeviceStatus, error) {
	tailnet, err := s.tailnet.Status(ctx)
	if err != nil {
		return protocol.DeviceStatus{}, err
	}
	config, ok := s.configStore.Current()
	if !ok {
		return protocol.DeviceStatus{}, errors.New("Agent 没有有效配置")
	}
	if !time.Now().UTC().Before(config.ExpiresAt) {
		return protocol.DeviceStatus{}, errors.New("Agent 签名配置已过期")
	}
	statuses := make([]protocol.ModuleStatus, 0, len(config.Modules))
	statusModuleIDs := make([]string, 0, len(config.Modules))
	for _, module := range config.Modules {
		if !module.Enabled {
			continue
		}
		spec, err := components.FindSpec(module.ID)
		if err != nil {
			return protocol.DeviceStatus{}, err
		}
		if version, managed := s.managedModuleVersions[module.ID]; managed {
			status := protocol.ModuleStatus{ID: spec.ID, State: "ready", Version: version, ExpectedVersion: spec.ExpectedVersion, CheckedAt: time.Now().UTC()}
			if version != spec.ExpectedVersion {
				status.State = "version_mismatch"
				status.Detail = "托管组件版本与固定版本不一致"
			}
			statuses = append(statuses, status)
		} else {
			statuses = append(statuses, components.Check(ctx, spec))
		}
		statuses[len(statuses)-1].ID = ModuleKey(module)
		statusModuleIDs = append(statusModuleIDs, module.ID)
	}
	var managedServices map[string]ServiceStatus
	if len(s.managedModuleVersions) > 0 {
		managedServices = make(map[string]ServiceStatus)
		servicesContext, servicesCancel := context.WithTimeout(ctx, 3*time.Second)
		services, servicesErr := s.system.Services(servicesContext)
		servicesCancel()
		if servicesErr != nil {
			for index, moduleID := range statusModuleIDs {
				if _, managed := s.managedModuleVersions[moduleID]; managed {
					statuses[index].State = "error"
					statuses[index].Detail = servicesErr.Error()
				}
			}
		} else {
			for _, service := range services {
				managedServices[service.ID] = service
			}
			for index, moduleID := range statusModuleIDs {
				if _, managed := s.managedModuleVersions[moduleID]; !managed {
					continue
				}
				service, exists := managedServices[moduleID]
				if !exists {
					statuses[index].State = "error"
					statuses[index].Detail = "托管组件服务状态缺失"
				} else if service.State != "active" {
					statuses[index].State = "error"
					statuses[index].Detail = service.Detail
				}
			}
		}
	}
	capabilities := []protocol.CapabilityStatus{{ID: "files", State: "disabled", Detail: "未配置共享目录"}, {ID: "media", State: "disabled", Detail: "Jellyfin 未启用"}, {ID: "system", State: "ready"}}
	if len(config.SharedDirectories) > 0 {
		s.proxyMu.RLock()
		files := s.files
		s.proxyMu.RUnlock()
		if files == nil {
			capabilities[0] = protocol.CapabilityStatus{ID: "files", State: "error", Detail: "文件服务未加载签名共享目录"}
		} else if err := files.Health(); err != nil {
			capabilities[0] = protocol.CapabilityStatus{ID: "files", State: "error", Detail: err.Error()}
		} else {
			capabilities[0] = protocol.CapabilityStatus{ID: "files", State: "ready"}
		}
	}
	for _, status := range statuses {
		if status.ID == "jellyfin" {
			state := status.State
			if state != "ready" {
				state = "error"
			}
			capabilities[1] = protocol.CapabilityStatus{ID: "media", State: state, Detail: status.Detail}
		}
	}
	_, managedJellyfin := s.managedModuleVersions["jellyfin"]
	if capabilities[1].State == "ready" && !managedJellyfin {
		servicesContext, servicesCancel := context.WithTimeout(ctx, 3*time.Second)
		services, servicesErr := s.system.Services(servicesContext)
		servicesCancel()
		if servicesErr != nil {
			capabilities[1] = protocol.CapabilityStatus{ID: "media", State: "error", Detail: servicesErr.Error()}
		} else {
			found := false
			for _, service := range services {
				if service.ID == "jellyfin" {
					found = true
					if service.State != "active" {
						capabilities[1] = protocol.CapabilityStatus{ID: "media", State: "error", Detail: service.Detail}
					}
				}
			}
			if !found {
				capabilities[1] = protocol.CapabilityStatus{ID: "media", State: "error", Detail: "Jellyfin 服务状态缺失"}
			}
		}
	}
	metricsContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := s.system.Metrics(metricsContext); err != nil {
		capabilities[2] = protocol.CapabilityStatus{ID: "system", State: "error", Detail: err.Error()}
	}
	return protocol.DeviceStatus{
		DeviceID: s.deviceID, Name: s.deviceName, Online: tailnet.Online,
		TailscaleIP: tailnet.TailscaleIP, Connection: tailnet.Connection,
		LastSeen: time.Now().UTC(), ConfigRevision: config.Revision, Modules: statuses, Capabilities: capabilities,
	}, nil
}

func (s *Server) dynamicProxy(moduleID string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.proxyMu.RLock()
		var proxy http.Handler
		proxy = s.mediaProxy
		s.proxyMu.RUnlock()
		if proxy == nil {
			writeAgentError(writer, http.StatusNotFound, "module_disabled", "模块未启用")
			return
		}
		proxy.ServeHTTP(writer, request)
	})
}

func (s *Server) fileResources(writer http.ResponseWriter, request *http.Request) {
	s.proxyMu.RLock()
	files := s.files
	s.proxyMu.RUnlock()
	if files == nil {
		writeAgentError(writer, http.StatusNotFound, "files_disabled", "未配置共享目录")
		return
	}
	resource, err := files.List(request.URL.Query().Get("path"))
	if err != nil {
		writeAgentError(writer, http.StatusBadRequest, "files_rejected", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusOK, resource)
}

func (s *Server) fileSearch(writer http.ResponseWriter, request *http.Request) {
	s.proxyMu.RLock()
	files := s.files
	s.proxyMu.RUnlock()
	if files == nil {
		writeAgentError(writer, http.StatusNotFound, "files_disabled", "未配置共享目录")
		return
	}
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeAgentError(writer, http.StatusBadRequest, "invalid_limit", "搜索结果数量无效")
			return
		}
		limit = value
	}
	results, err := files.Search(request.URL.Query().Get("q"), limit)
	if err != nil {
		writeAgentError(writer, http.StatusBadRequest, "search_rejected", err.Error())
		return
	}
	writeAgentJSON(writer, http.StatusOK, map[string]any{"items": results})
}

func (s *Server) fileRaw(writer http.ResponseWriter, request *http.Request) {
	s.proxyMu.RLock()
	files := s.files
	s.proxyMu.RUnlock()
	if files == nil {
		writeAgentError(writer, http.StatusNotFound, "files_disabled", "未配置共享目录")
		return
	}
	path, info, err := files.ResolveFile(request.URL.Query().Get("files"))
	if err != nil {
		writeAgentError(writer, http.StatusBadRequest, "files_rejected", err.Error())
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeAgentError(writer, http.StatusInternalServerError, "files_failed", err.Error())
		return
	}
	defer file.Close()
	disposition := "inline"
	if request.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	writer.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": info.Name()}))
	http.ServeContent(writer, request, info.Name(), info.ModTime(), file)
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie("homestack_session")
		if err != nil || !s.sessions.Valid(cookie.Value) {
			writeAgentError(writer, http.StatusUnauthorized, "session_required", "需要有效的设备访问会话")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func prefixProxy(incomingPrefix, upstreamPrefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cloned := request.Clone(request.Context())
		cloned.URL.Path = upstreamPrefix + strings.TrimPrefix(request.URL.Path, incomingPrefix)
		cloned.URL.RawPath = ""
		next.ServeHTTP(writer, cloned)
	})
}

func writeAgentJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func agentSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		next.ServeHTTP(writer, request)
	})
}

func ListenHTTP(address string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || port != "19444" {
		return nil, errors.New("Node 后端必须监听明确的回环地址 19444")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("监听 Node 回环端口失败: %w", err)
	}
	return listener, nil
}

func ServeHTTP(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := newStreamingServer(handler)
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
