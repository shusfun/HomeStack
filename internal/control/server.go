package control

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/publicurl"
	"github.com/wangshangbin/homestack/internal/secure"
	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

type ServerOptions struct {
	Authenticator Authenticator
	Owners        *OwnerStore
	Devices       *DeviceStore
	Activations   *ActivationStore
	Registration  *RegistrationService
	ConfigHelper  setupapi.ConfigHelper
	SigningKey    ed25519.PrivateKey
	SigningKeyID  string
	PublicURL     string
	Now           func() time.Time
	Random        io.Reader
}

type Server struct {
	authenticator Authenticator
	owners        *OwnerStore
	devices       *DeviceStore
	activations   *ActivationStore
	registration  *RegistrationService
	configHelper  setupapi.ConfigHelper
	signingKey    ed25519.PrivateKey
	signingKeyID  string
	publicURL     string
	now           func() time.Time
	random        io.Reader
}

type ticketResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type currentUserResponse struct {
	Subject    string        `json:"subject"`
	Email      string        `json:"email"`
	Name       string        `json:"name"`
	Identities []IdentityKey `json:"identities"`
}

type appTokenIssuer interface {
	IssueAppTokens(string) (AppTokens, error)
}

type reauthAuthorizer interface {
	ConsumeReauth(*http.Request, string) bool
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Authenticator == nil || options.Owners == nil || options.Devices == nil {
		return nil, errors.New("Control 依赖未完整配置")
	}
	if len(options.Authenticator.Metadata()) == 0 {
		return nil, errors.New("Control 必须至少配置一个可用的 OAuth 登录方式")
	}
	if len(options.SigningKey) != ed25519.PrivateKeySize || options.SigningKeyID == "" {
		return nil, errors.New("Control Ed25519 签名密钥配置无效")
	}
	parsed, err := url.Parse(options.PublicURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("public_url 必须是无凭据、无路径的有效 HTTPS 地址")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Activations == nil {
		options.Activations, err = OpenActivationStore("", options.Now, options.Random)
		if err != nil {
			return nil, err
		}
	}
	if options.Registration == nil {
		options.Registration, err = NewRegistrationService(options.Devices, options.SigningKey, options.SigningKeyID, options.PublicURL, options.Now, options.Random)
		if err != nil {
			return nil, err
		}
	}
	server := &Server{authenticator: options.Authenticator, owners: options.Owners, devices: options.Devices, activations: options.Activations, registration: options.Registration, configHelper: options.ConfigHelper, signingKey: options.SigningKey, signingKeyID: options.SigningKeyID, publicURL: strings.TrimRight(options.PublicURL, "/"), now: options.Now, random: options.Random}
	if manager, ok := options.Authenticator.(*AuthManager); ok && options.ConfigHelper != nil {
		manager.SetProviderLinker(providerLinkService{owners: options.Owners, helper: options.ConfigHelper})
	}
	return server, nil
}

func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/setup/status", setupLocked)
	mux.HandleFunc("POST /api/setup/session", setupLocked)
	mux.HandleFunc("POST /api/setup/prepare", setupLocked)
	mux.HandleFunc("POST /api/setup/finalize", setupLocked)
	mux.HandleFunc("GET /api/health", s.health)
	if webAuth, ok := s.authenticator.(WebAuthenticator); ok {
		webAuth.RegisterRoutes(mux)
	}
	mux.HandleFunc("GET /api/meta", s.meta)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("GET /api/system/config", s.systemConfig)
	mux.HandleFunc("POST /api/system/reconfigure", s.reconfigure)
	mux.HandleFunc("POST /api/system/providers/{provider}/link", s.linkProvider)
	mux.HandleFunc("POST /api/device-activations", s.createActivation)
	mux.HandleFunc("POST /api/auth/app/activate", s.activateApp)
	mux.HandleFunc("POST /api/devices/register", s.registerDevice)
	mux.HandleFunc("GET /api/devices", s.listDevices)
	mux.HandleFunc("DELETE /api/devices/{deviceID}", s.removeDevice)
	mux.HandleFunc("POST /api/devices/{deviceID}/tickets", s.createTicket)
	mux.HandleFunc("GET /devices/{deviceID}/open", s.openDevice)
	mux.HandleFunc("PUT /api/devices/{deviceID}/status", s.updateStatus)
	mux.HandleFunc("GET /api/device/config", s.getDeviceConfig)
	if static != nil {
		mux.Handle("/", static)
	}
	return securityHeaders(mux)
}

func (s *Server) systemConfig(writer http.ResponseWriter, request *http.Request) {
	if _, ok := s.requireIdentity(writer, request); !ok {
		return
	}
	if s.configHelper == nil {
		writeControlError(writer, http.StatusServiceUnavailable, "config_helper_unavailable", "Config Helper 未配置")
		return
	}
	config, err := s.configHelper.Configuration(request.Context())
	if err != nil {
		writeControlError(writer, http.StatusBadGateway, "config_failed", err.Error())
		return
	}
	owner, _ := s.owners.Owner()
	configured := map[string]setupapi.PublicProviderConfiguration{}
	for _, provider := range config.Providers {
		configured[provider.ID] = provider
	}
	providers := make([]map[string]any, 0, 2)
	for _, id := range []string{"google", "github"} {
		provider, exists := configured[id]
		providers = append(providers, map[string]any{"id": id, "label": providerLabel(id), "configured": exists, "linked": ownerHasProvider(owner, id), "client_id": provider.ClientID})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"public_host": config.PublicHost, "providers": providers})
}

func (s *Server) reconfigure(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	if request.Header.Get("Authorization") != "" || !s.validWriteOrigin(request) {
		writeControlError(writer, http.StatusForbidden, "origin_rejected", "域名迁移只接受当前浏览器会话")
		return
	}
	authorizer, ok := s.authenticator.(reauthAuthorizer)
	if !ok || !authorizer.ConsumeReauth(request, identity.Subject) {
		writeControlError(writer, http.StatusForbidden, "reauthentication_required", "域名迁移需要重新认证")
		return
	}
	if s.configHelper == nil {
		writeControlError(writer, http.StatusServiceUnavailable, "config_helper_unavailable", "Config Helper 未配置")
		return
	}
	var body struct {
		PublicHost   string `json:"public_host"`
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	address, err := publicurl.Normalize(body.PublicHost)
	if err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_configuration", err.Error())
		return
	}
	confirmation, err := publicurl.Normalize(body.Confirmation)
	if err != nil || confirmation.Host != address.Host {
		writeControlError(writer, http.StatusBadRequest, "confirmation_mismatch", "确认文本必须与新域名一致")
		return
	}
	current, err := s.configHelper.Configuration(request.Context())
	if err != nil {
		writeControlError(writer, http.StatusBadGateway, "config_failed", err.Error())
		return
	}
	if err := preflightControlDomain(request.Context(), address.Host); err != nil {
		writeControlError(writer, http.StatusBadRequest, "preflight_failed", err.Error())
		return
	}
	status, err := s.configHelper.ReconfigureDomain(request.Context(), address.Host)
	if err != nil {
		writeControlError(writer, http.StatusBadGateway, "reconfigure_failed", err.Error())
		return
	}
	newURL := address.URL
	if err := s.devices.UpdateControlURL(newURL, s.now().UTC(), func(deviceConfig protocol.SignedDeviceConfig) (string, error) {
		return secure.SignJWS(s.signingKey, s.signingKeyID, deviceConfig)
	}); err != nil {
		_, rollbackErr := s.configHelper.ReconfigureDomain(request.Context(), current.PublicHost)
		if rollbackErr != nil {
			err = fmt.Errorf("%v；恢复旧域名失败: %w", err, rollbackErr)
		}
		writeControlError(writer, http.StatusInternalServerError, "device_config_failed", err.Error())
		return
	}
	if err := s.owners.RevokeAllSessions(); err != nil {
		writeControlError(writer, http.StatusInternalServerError, "session_revoke_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"status": status, "target_url": newURL})
}

func (s *Server) linkProvider(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	if request.Header.Get("Authorization") != "" || !s.validWriteOrigin(request) {
		writeControlError(writer, http.StatusForbidden, "origin_rejected", "绑定登录方式只接受当前浏览器会话")
		return
	}
	if s.configHelper == nil {
		writeControlError(writer, http.StatusServiceUnavailable, "config_helper_unavailable", "Config Helper 未配置")
		return
	}
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	providerID := strings.ToLower(strings.TrimSpace(request.PathValue("provider")))
	body.ClientID, body.ClientSecret = strings.TrimSpace(body.ClientID), strings.TrimSpace(body.ClientSecret)
	if body.Confirmation != providerID {
		writeControlError(writer, http.StatusBadRequest, "confirmation_mismatch", "确认文本必须与新登录方式 ID 一致")
		return
	}
	current, err := s.configHelper.Configuration(request.Context())
	if err != nil {
		writeControlError(writer, http.StatusBadGateway, "config_failed", err.Error())
		return
	}
	for _, configured := range current.Providers {
		if configured.ID == providerID {
			writeControlError(writer, http.StatusConflict, "provider_configured", "该登录方式已经配置")
			return
		}
	}
	config, err := setupapi.NormalizeConfiguration(setupapi.Configuration{PublicHost: current.PublicHost, Providers: map[string]setupapi.ProviderCredentials{providerID: {ClientID: body.ClientID, ClientSecret: body.ClientSecret}}})
	if err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_configuration", err.Error())
		return
	}
	credentials := config.Providers[providerID]
	var provider *OAuthProvider
	if providerID == "google" {
		provider, err = NewOIDCProvider(request.Context(), "google", "Google", "https://accounts.google.com", credentials.ClientID, credentials.ClientSecret, s.publicURL)
	} else if providerID == "github" {
		provider, err = NewGitHubProvider(credentials.ClientID, credentials.ClientSecret, s.publicURL)
	} else {
		err = errors.New("登录方式只能是 Google 或 GitHub")
	}
	if err != nil {
		writeControlError(writer, http.StatusBadGateway, "provider_failed", err.Error())
		return
	}
	authorizer, authorized := s.authenticator.(reauthAuthorizer)
	starter, supported := s.authenticator.(interface {
		BeginProviderLink(string, *OAuthProvider, setupapi.ProviderCredentials) (string, error)
	})
	if !authorized || !supported || !authorizer.ConsumeReauth(request, identity.Subject) {
		writeControlError(writer, http.StatusForbidden, "reauthentication_required", "绑定登录方式需要先用当前登录方式重新认证")
		return
	}
	authorizationURL, err := starter.BeginProviderLink(identity.Subject, provider, credentials)
	if err != nil {
		writeControlError(writer, http.StatusBadRequest, "provider_link_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]string{"authorization_url": authorizationURL})
}

func preflightControlDomain(ctx context.Context, host string) error {
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("解析新域名失败: %w", err)
	}
	public := false
	for _, address := range addresses {
		if address.IsGlobalUnicast() && !address.IsPrivate() {
			public = true
			break
		}
	}
	if !public {
		return errors.New("新域名未解析到公网地址")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/api/health", nil)
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("新域名 TLS 或反向代理检查失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("新域名健康检查返回 HTTP %d", response.StatusCode)
	}
	return nil
}

func setupLocked(writer http.ResponseWriter, _ *http.Request) {
	writeControlError(writer, http.StatusLocked, "setup_locked", "Setup 已完成并永久锁定")
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) meta(writer http.ResponseWriter, request *http.Request) {
	payload := map[string]any{
		"surface": "control", "providers": s.authenticator.Metadata(), "signing_key_id": s.signingKeyID,
		"signing_public_key": base64.RawURLEncoding.EncodeToString(s.signingKey.Public().(ed25519.PublicKey)),
		"node":               map[string]any{"backend": "127.0.0.1:19444", "serve_port": 19443},
		"me":                 nil,
	}
	if identity, err := s.authenticator.Authenticate(request.Context(), request); err == nil {
		if owner, exists := s.owners.Owner(); exists && owner.ID == identity.Subject {
			payload["me"] = currentUserResponse{Subject: identity.Subject, Email: identity.Email, Name: identity.Name, Identities: owner.Identities}
		}
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, payload)
}

func (s *Server) me(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	owner, exists := s.owners.Owner()
	if !exists || owner.ID != identity.Subject {
		writeControlError(writer, http.StatusUnauthorized, "owner_missing", "当前所有者不存在")
		return
	}
	writeJSON(writer, http.StatusOK, currentUserResponse{Subject: identity.Subject, Email: identity.Email, Name: identity.Name, Identities: owner.Identities})
}

func (s *Server) createActivation(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	if !s.validWriteOrigin(request) {
		writeControlError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Control 地址不匹配")
		return
	}
	code, expiresAt, err := s.activations.Create(identity.Subject)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrActivationExists) {
			status = http.StatusConflict
		}
		writeControlError(writer, status, "activation_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"code": code, "expires_at": expiresAt})
}

func (s *Server) activateApp(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Code string                    `json:"code"`
		Node protocol.NodeRegistration `json:"node"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ownerID, err := s.activations.Redeem(strings.TrimSpace(body.Code))
	if err != nil {
		writeControlError(writer, http.StatusUnauthorized, "activation_rejected", err.Error())
		return
	}
	registration, err := s.registration.Register(ownerID, body.Node)
	if err != nil {
		writeControlError(writer, http.StatusBadRequest, "registration_failed", err.Error())
		return
	}
	issuer, ok := s.authenticator.(appTokenIssuer)
	if !ok {
		writeControlError(writer, http.StatusServiceUnavailable, "app_auth_unavailable", "App 会话签发器未配置")
		return
	}
	tokens, err := issuer.IssueAppTokens(ownerID)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "token_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"session": tokens, "registration": registration})
}

func (s *Server) registerDevice(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	var body protocol.NodeRegistration
	if err := decodeJSONBody(writer, request, &body); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	registered, err := s.registration.Register(identity.Subject, body)
	if err != nil {
		writeControlError(writer, http.StatusBadRequest, "registration_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, registered)
}

func (s *Server) listDevices(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"devices": s.devices.List(identity.Subject)})
}

func (s *Server) removeDevice(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	if !s.validWriteOrigin(request) {
		writeControlError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Control 地址不匹配")
		return
	}
	if err := s.devices.Remove(request.PathValue("deviceID"), identity.Subject); err != nil {
		writeControlError(writer, http.StatusForbidden, "device_denied", err.Error())
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) createTicket(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	if !s.validWriteOrigin(request) {
		writeControlError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Control 地址不匹配")
		return
	}
	record, err := s.devices.Owned(request.PathValue("deviceID"), identity.Subject)
	if err != nil {
		writeControlError(writer, http.StatusForbidden, "device_denied", err.Error())
		return
	}
	target, expiresAt, err := s.signedAgentURL(identity, record)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "ticket_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, ticketResponse{URL: target, ExpiresAt: expiresAt})
}

func (s *Server) openDevice(writer http.ResponseWriter, request *http.Request) {
	identity, err := s.authenticator.Authenticate(request.Context(), request)
	if err != nil {
		provider := s.authenticator.Metadata()[0]
		returnPath := "/devices/" + url.PathEscape(request.PathValue("deviceID")) + "/open"
		http.Redirect(writer, request, "/auth/login/"+url.PathEscape(provider.ID)+"?return="+url.QueryEscape(returnPath), http.StatusSeeOther)
		return
	}
	record, err := s.devices.Owned(request.PathValue("deviceID"), identity.Subject)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusForbidden)
		return
	}
	target, _, err := s.signedAgentURL(identity, record)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(writer, request, target, http.StatusSeeOther)
}

func (s *Server) signedAgentURL(identity Identity, record DeviceRecord) (string, time.Time, error) {
	nonce, err := randomToken(s.random, 24)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now().UTC()
	claims := protocol.AccessTicketClaims{Issuer: s.publicURL, Subject: identity.Subject, DeviceID: record.ID, Nonce: nonce, IssuedAt: now, ExpiresAt: now.Add(30 * time.Second)}
	ticket, err := secure.SignJWS(s.signingKey, s.signingKeyID, claims)
	if err != nil {
		return "", time.Time{}, err
	}
	target, err := url.Parse(record.AgentURL)
	if err != nil {
		return "", time.Time{}, err
	}
	target.Path = "/access"
	query := target.Query()
	query.Set("ticket", ticket)
	target.RawQuery = query.Encode()
	return target.String(), claims.ExpiresAt, nil
}

func (s *Server) updateStatus(writer http.ResponseWriter, request *http.Request) {
	deviceID := request.PathValue("deviceID")
	record, ok := s.requireDevice(writer, request, deviceID)
	if !ok {
		return
	}
	var status protocol.DeviceStatus
	if err := decodeJSONBody(writer, request, &status); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if status.DeviceID != deviceID || status.TailscaleIP != record.TailscaleIP {
		writeControlError(writer, http.StatusBadRequest, "invalid_status", "设备状态与登记信息不匹配")
		return
	}
	status.LastSeen = s.now().UTC()
	if err := s.devices.UpdateStatus(deviceID, status); err != nil {
		writeControlError(writer, http.StatusInternalServerError, "status_failed", err.Error())
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) getDeviceConfig(writer http.ResponseWriter, request *http.Request) {
	deviceID := request.URL.Query().Get("device_id")
	if _, ok := s.requireDevice(writer, request, deviceID); !ok {
		return
	}
	signed, err := s.devices.RotateConfig(deviceID, s.now().UTC(), func(config protocol.SignedDeviceConfig) (string, error) {
		return secure.SignJWS(s.signingKey, s.signingKeyID, config)
	})
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "config_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"signed_config": signed})
}

func (s *Server) requireIdentity(writer http.ResponseWriter, request *http.Request) (Identity, bool) {
	identity, err := s.authenticator.Authenticate(request.Context(), request)
	if err != nil {
		writeControlError(writer, http.StatusUnauthorized, "unauthenticated", err.Error())
		return Identity{}, false
	}
	return identity, true
}

func (s *Server) requireDevice(writer http.ResponseWriter, request *http.Request, deviceID string) (DeviceRecord, bool) {
	parts := strings.Fields(request.Header.Get("Authorization"))
	if len(parts) != 2 || parts[0] != "Device" {
		writeControlError(writer, http.StatusUnauthorized, "device_unauthenticated", "缺少设备认证")
		return DeviceRecord{}, false
	}
	record, err := s.devices.Authenticate(deviceID, parts[1])
	if err != nil {
		writeControlError(writer, http.StatusUnauthorized, "device_unauthenticated", err.Error())
		return DeviceRecord{}, false
	}
	return record, true
}

func (s *Server) validWriteOrigin(request *http.Request) bool {
	if strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	origin := request.Header.Get("Origin")
	return origin != "" && strings.TrimRight(origin, "/") == s.publicURL
}

func decodeJSONBody(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("解析 JSON 请求失败: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON 请求只能包含一个对象")
	}
	return nil
}

func randomToken(reader io.Reader, size int) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", fmt.Errorf("生成安全随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeControlError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, protocol.ErrorResponse{Error: protocol.APIError{Code: code, Message: message}})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		next.ServeHTTP(writer, request)
	})
}

func ServeReverseProxy(ctx context.Context, address string, handler http.Handler) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("reverse-proxy 模式必须绑定明确的回环 IP 和端口")
	}
	return serve(ctx, &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute})
}

func serve(ctx context.Context, server *http.Server) error {
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
