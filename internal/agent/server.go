package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	DeviceID      string
	DeviceName    string
	ConfigStore   *ConfigStore
	Sessions      *SessionStore
	Tailnet       TailnetStatus
	ModuleSecrets map[string]map[string]string
}

type Server struct {
	deviceID    string
	deviceName  string
	configStore *ConfigStore
	sessions    *SessionStore
	tailnet     TailnetStatus
	proxyMu     sync.RWMutex
	secrets     map[string]map[string]string
	fileProxy   http.Handler
	mediaProxy  http.Handler
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.DeviceID == "" || options.DeviceName == "" || options.ConfigStore == nil || options.Sessions == nil || options.Tailnet == nil {
		return nil, errors.New("Agent 依赖未完整配置")
	}
	_, ok := options.ConfigStore.Current()
	if !ok {
		return nil, errors.New("Agent 尚未获得有效签名配置")
	}
	server := &Server{
		deviceID: options.DeviceID, deviceName: options.DeviceName, configStore: options.ConfigStore,
		sessions: options.Sessions, tailnet: options.Tailnet, secrets: options.ModuleSecrets,
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
	var fileProxy http.Handler
	var mediaProxy http.Handler
	for _, module := range config.Modules {
		if !module.Enabled {
			continue
		}
		switch module.ID {
		case "filebrowser":
			token := s.secrets[ModuleKey(module)]["api_token"]
			if token == "" {
				return errors.New("FileBrowser 模块缺少 api_token")
			}
			proxy, err := NewFileBrowserProxy(module.BaseURL, token)
			if err != nil {
				return err
			}
			fileProxy = prefixProxy("/api/v1/files", "/api", proxy)
		case "jellyfin":
			apiKey := s.secrets[ModuleKey(module)]["api_key"]
			if apiKey == "" {
				return errors.New("Jellyfin 模块缺少 api_key")
			}
			proxy, err := NewJellyfinProxy(module.BaseURL, apiKey)
			if err != nil {
				return err
			}
			mediaProxy = prefixProxy("/api/v1/media", "", proxy)
		}
	}
	s.proxyMu.Lock()
	s.fileProxy = fileProxy
	s.mediaProxy = mediaProxy
	s.proxyMu.Unlock()
	return nil
}

func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /access", s.redeemTicket)
	mux.HandleFunc("GET /api/v1/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeAgentJSON(writer, http.StatusOK, map[string]string{"status": "ok", "version": "v1"})
	})
	mux.Handle("GET /api/v1/status", s.requireSession(s.requireActiveConfig(http.HandlerFunc(s.status))))
	mux.Handle("/api/v1/files/", s.requireSession(s.requireActiveConfig(s.dynamicProxy("filebrowser"))))
	mux.Handle("/api/v1/media/", s.requireSession(s.requireActiveConfig(s.dynamicProxy("jellyfin"))))
	if static != nil {
		mux.Handle("/", static)
	}
	return agentSecurityHeaders(mux)
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
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
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

func (s *Server) BuildStatus(ctx context.Context) (protocol.DeviceStatusV1, error) {
	tailnet, err := s.tailnet.Status(ctx)
	if err != nil {
		return protocol.DeviceStatusV1{}, err
	}
	config, ok := s.configStore.Current()
	if !ok {
		return protocol.DeviceStatusV1{}, errors.New("Agent 没有有效配置")
	}
	if !time.Now().UTC().Before(config.ExpiresAt) {
		return protocol.DeviceStatusV1{}, errors.New("Agent 签名配置已过期")
	}
	statuses := make([]protocol.ModuleStatusV1, 0, len(config.Modules))
	for _, module := range config.Modules {
		if !module.Enabled {
			continue
		}
		spec, err := components.FindSpec(module.ID)
		if err != nil {
			return protocol.DeviceStatusV1{}, err
		}
		statuses = append(statuses, components.Check(ctx, spec))
		statuses[len(statuses)-1].ID = ModuleKey(module)
	}
	return protocol.DeviceStatusV1{
		Version: protocol.DeviceStatusVersion, DeviceID: s.deviceID, Name: s.deviceName, Online: tailnet.Online,
		TailnetIP: tailnet.TailnetIP, Connection: tailnet.Connection, DERPRegion: tailnet.DERPRegion,
		LastSeen: time.Now().UTC(), ConfigRevision: config.Revision, Modules: statuses,
	}, nil
}

func (s *Server) dynamicProxy(moduleID string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.proxyMu.RLock()
		var proxy http.Handler
		if moduleID == "filebrowser" {
			proxy = s.fileProxy
		} else {
			proxy = s.mediaProxy
		}
		s.proxyMu.RUnlock()
		if proxy == nil {
			writeAgentError(writer, http.StatusNotFound, "module_disabled", "模块未启用")
			return
		}
		proxy.ServeHTTP(writer, request)
	})
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

func ServeTLS(ctx context.Context, address, certFile, keyFile string, handler http.Handler) error {
	server := newStreamingServer(handler)
	server.Addr = address
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	if err := server.ListenAndServeTLS(certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
