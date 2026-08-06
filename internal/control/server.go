package control

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/agent"
	"github.com/wangshangbin/homestack/internal/invite"
	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
)

type ServerOptions struct {
	Authenticator Authenticator
	Owners        *OwnerStore
	Invites       *invite.Store
	Devices       *DeviceStore
	Headscale     Headscale
	SigningKey    ed25519.PrivateKey
	SigningKeyID  string
	PublicURL     string
	HeadscaleURL  string
	Now           func() time.Time
	Random        io.Reader
}

type Server struct {
	authenticator Authenticator
	owners        *OwnerStore
	invites       *invite.Store
	devices       *DeviceStore
	headscale     Headscale
	signingKey    ed25519.PrivateKey
	signingKeyID  string
	publicURL     string
	headscaleURL  string
	now           func() time.Time
	random        io.Reader
}

type createInviteResponse struct {
	JoinInfo  string    `json:"join_info"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ticketResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Authenticator == nil || options.Owners == nil || options.Invites == nil || options.Devices == nil || options.Headscale == nil {
		return nil, errors.New("Control 依赖未完整配置")
	}
	if len(options.SigningKey) != ed25519.PrivateKeySize || options.SigningKeyID == "" {
		return nil, errors.New("Control Ed25519 签名密钥配置无效")
	}
	for name, raw := range map[string]string{"public_url": options.PublicURL, "headscale_url": options.HeadscaleURL} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("%s 必须是无凭据、无路径的有效 HTTPS 地址", name)
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Server{
		authenticator: options.Authenticator, owners: options.Owners, invites: options.Invites, devices: options.Devices, headscale: options.Headscale,
		signingKey: options.SigningKey, signingKeyID: options.SigningKeyID, publicURL: options.PublicURL,
		headscaleURL: options.HeadscaleURL, now: options.Now, random: options.Random,
	}, nil
}

func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	if webAuth, ok := s.authenticator.(WebAuthenticator); ok {
		webAuth.RegisterRoutes(mux)
	}
	mux.HandleFunc("GET /api/v1/meta", s.meta)
	mux.HandleFunc("GET /api/v1/me", s.me)
	mux.HandleFunc("POST /api/v1/device-enrollments", s.createEnrollment)
	mux.HandleFunc("POST /api/v1/tailnet/auth-keys", s.createTailnetAuthKey)
	mux.HandleFunc("POST /api/v1/join/exchange", s.exchangeJoin)
	mux.HandleFunc("GET /api/v1/devices", s.listDevices)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/tickets", s.createTicket)
	mux.HandleFunc("GET /devices/{deviceID}/open", s.openDevice)
	mux.HandleFunc("PUT /api/v1/devices/{deviceID}/status", s.updateStatus)
	mux.HandleFunc("GET /api/v1/device/config", s.getDeviceConfig)
	if static != nil {
		mux.Handle("/", static)
	}
	return securityHeaders(mux)
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok", "version": "v1"})
}

func (s *Server) meta(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"version":            "v1",
		"providers":          s.authenticator.Metadata(),
		"signing_key_id":     s.signingKeyID,
		"signing_public_key": base64.RawURLEncoding.EncodeToString(s.signingKey.Public().(ed25519.PublicKey)),
		"components": map[string]string{
			"wails": "3.0.0-beta.4", "headscale": "0.29.3", "tailscale": "1.102.2", "pocket_id": "2.12.0",
			"filebrowser": "0.3.5", "jellyfin": "10.11.11", "cc_connect": "1.4.1",
		},
	})
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
	writeJSON(writer, http.StatusOK, map[string]any{
		"subject": identity.Subject, "email": identity.Email, "name": identity.Name, "identities": owner.Identities,
	})
}

func (s *Server) createEnrollment(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	if !s.validWriteOrigin(request) {
		writeControlError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Control 地址不匹配")
		return
	}
	var policy protocol.JoinPolicyV1
	if err := decodeJSONBody(writer, request, &policy); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateJoinPolicy(policy); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	payload, err := json.Marshal(policy)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "encode_failed", err.Error())
		return
	}
	descriptor, record, err := s.invites.Create(s.publicURL, identity.Subject, 10*time.Minute, payload)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "enrollment_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, createInviteResponse{JoinInfo: descriptor.String(), ExpiresAt: record.ExpiresAt})
}

func (s *Server) createTailnetAuthKey(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	if !s.validWriteOrigin(request) {
		writeControlError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Control 地址不匹配")
		return
	}
	authKey, err := s.headscale.CreateSingleUseKey(request.Context(), identity.Email)
	if err != nil {
		writeControlError(writer, http.StatusBadGateway, "headscale_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"login_server": s.headscaleURL,
		"auth_key":     authKey,
		"expires_at":   s.now().UTC().Add(10 * time.Minute),
	})
}

func (s *Server) exchangeJoin(writer http.ResponseWriter, request *http.Request) {
	var joinRequest protocol.JoinRequestV1
	if err := decodeJSONBody(writer, request, &joinRequest); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if joinRequest.Version != protocol.JoinVersion {
		writeControlError(writer, http.StatusBadRequest, "unsupported_version", "连接协议版本不受支持")
		return
	}
	if _, err := protocol.NewJoinDescriptor(s.publicURL, joinRequest.Code); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_code", err.Error())
		return
	}
	publicBytes, err := base64.RawURLEncoding.DecodeString(joinRequest.EncryptionPublicKey)
	if err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_key", "设备 X25519 公钥编码无效")
		return
	}
	publicKey, err := ecdh.X25519().NewPublicKey(publicBytes)
	if err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_key", "设备 X25519 公钥无效")
		return
	}
	record, err := s.invites.Redeem(joinRequest.Code)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, invite.ErrExpired) || errors.Is(err, invite.ErrUsed) {
			status = http.StatusGone
		}
		writeControlError(writer, status, "invite_rejected", err.Error())
		return
	}
	var policy protocol.JoinPolicyV1
	if err := json.Unmarshal(record.Payload, &policy); err != nil {
		writeControlError(writer, http.StatusInternalServerError, "policy_failed", "邀请码策略无法解析")
		return
	}
	if joinRequest.DeviceName != policy.DeviceName || joinRequest.AgentURL != policy.AgentURL {
		writeControlError(writer, http.StatusBadRequest, "policy_mismatch", "设备名称或 Agent 地址与配对策略不匹配")
		return
	}
	owner, ok := s.ownerForSubject(record.CreatedBy)
	if !ok {
		writeControlError(writer, http.StatusUnauthorized, "owner_missing", "配对码所属的 HomeStack 所有者不存在")
		return
	}
	authKey, err := s.headscale.CreateSingleUseKey(request.Context(), owner.Email)
	if err != nil {
		writeControlError(writer, http.StatusBadGateway, "headscale_failed", err.Error())
		return
	}
	deviceID, err := randomToken(s.random, 16)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "random_failed", err.Error())
		return
	}
	deviceToken, err := randomToken(s.random, 32)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "random_failed", err.Error())
		return
	}
	now := s.now().UTC()
	deviceConfig := protocol.SignedDeviceConfigV1{
		Version: protocol.DeviceConfigVersion, DeviceID: deviceID, DeviceName: policy.DeviceName, Revision: 1, IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		ControlURL: s.publicURL, AgentURL: policy.AgentURL, Modules: policy.Modules, SharedDirectories: policy.SharedDirectories,
	}
	if err := agent.ValidateDeviceConfig(deviceConfig); err != nil {
		writeControlError(writer, http.StatusInternalServerError, "policy_failed", err.Error())
		return
	}
	signedConfig, err := secure.SignJWS(s.signingKey, s.signingKeyID, deviceConfig)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "sign_failed", err.Error())
		return
	}
	credential := protocol.DeviceCredentialV1{
		Version: protocol.JoinVersion, DeviceID: deviceID, DeviceToken: deviceToken, HeadscaleLoginServer: s.headscaleURL,
		HeadscaleAuthKey: authKey, ModuleSecrets: policy.ModuleSecrets, ExpiresAt: now.Add(10 * time.Minute),
	}
	sealed, err := secure.SealJSON(publicKey, credential)
	if err != nil {
		writeControlError(writer, http.StatusInternalServerError, "seal_failed", err.Error())
		return
	}
	if err := s.devices.Add(DeviceRecord{
		ID: deviceID, Name: policy.DeviceName, OwnerSubject: owner.ID, OwnerEmail: owner.Email, AgentURL: policy.AgentURL,
		Config: deviceConfig, SignedConfig: signedConfig, CreatedAt: now,
	}, deviceToken); err != nil {
		writeControlError(writer, http.StatusInternalServerError, "device_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, protocol.JoinResponseV1{
		Version: protocol.JoinVersion, DeviceID: deviceID, DeviceName: policy.DeviceName, SealedCredential: sealed, SignedConfig: signedConfig,
	})
}

func (s *Server) listDevices(writer http.ResponseWriter, request *http.Request) {
	identity, ok := s.requireIdentity(writer, request)
	if !ok {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"devices": s.devices.List(identity.Subject)})
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
		providers := s.authenticator.Metadata()
		if len(providers) == 0 {
			http.Error(writer, "Control 未配置登录方式", http.StatusServiceUnavailable)
			return
		}
		returnPath := "/devices/" + url.PathEscape(request.PathValue("deviceID")) + "/open"
		login := "/auth/login/" + url.PathEscape(providers[0].ID) + "?return=" + url.QueryEscape(returnPath)
		http.Redirect(writer, request, login, http.StatusSeeOther)
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
	claims := protocol.AccessTicketClaimsV1{
		Version: protocol.AccessTicketVersion, Issuer: s.publicURL, Subject: identity.Subject, DeviceID: record.ID,
		Nonce: nonce, IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}
	ticket, err := secure.SignJWS(s.signingKey, s.signingKeyID, claims)
	if err != nil {
		return "", time.Time{}, err
	}
	target, err := url.Parse(record.AgentURL)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("解析 Agent 地址失败: %w", err)
	}
	target.Path = "/access"
	query := target.Query()
	query.Set("ticket", ticket)
	target.RawQuery = query.Encode()
	return target.String(), claims.ExpiresAt, nil
}

func (s *Server) ownerForSubject(subject string) (Owner, bool) {
	owner, ok := s.owners.Owner()
	return owner, ok && owner.ID == subject
}

func (s *Server) validWriteOrigin(request *http.Request) bool {
	if strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	origin := request.Header.Get("Origin")
	return origin != "" && strings.TrimRight(origin, "/") == strings.TrimRight(s.publicURL, "/")
}

func (s *Server) updateStatus(writer http.ResponseWriter, request *http.Request) {
	deviceID := request.PathValue("deviceID")
	if _, ok := s.requireDevice(writer, request, deviceID); !ok {
		return
	}
	var status protocol.DeviceStatusV1
	if err := decodeJSONBody(writer, request, &status); err != nil {
		writeControlError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if status.Version != protocol.DeviceStatusVersion || status.DeviceID != deviceID {
		writeControlError(writer, http.StatusBadRequest, "invalid_status", "设备状态版本或设备 ID 不匹配")
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
	_, ok := s.requireDevice(writer, request, deviceID)
	if !ok {
		return
	}
	signed, err := s.devices.RotateConfig(deviceID, s.now().UTC(), func(config protocol.SignedDeviceConfigV1) (string, error) {
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

func validateJoinPolicy(policy protocol.JoinPolicyV1) error {
	if strings.TrimSpace(policy.DeviceName) == "" || len(policy.DeviceName) > 80 {
		return errors.New("设备名称不能为空且不能超过 80 个字符")
	}
	agentURL, err := url.Parse(policy.AgentURL)
	if err != nil || agentURL.Scheme != "https" || agentURL.Hostname() == "" || agentURL.User != nil ||
		agentURL.Port() != "9443" || (agentURL.Path != "" && agentURL.Path != "/") || agentURL.RawQuery != "" || agentURL.Fragment != "" {
		return errors.New("Agent 地址必须是无凭据、无路径的 HTTPS 地址，并明确使用 9443 端口")
	}
	config := protocol.SignedDeviceConfigV1{
		Version: protocol.DeviceConfigVersion, DeviceID: "validation", DeviceName: policy.DeviceName, Revision: 1,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), ControlURL: "https://control.invalid:8443", AgentURL: policy.AgentURL,
		Modules: policy.Modules, SharedDirectories: policy.SharedDirectories,
	}
	if err := agent.ValidateDeviceConfig(config); err != nil {
		return err
	}
	return validateModuleSecrets(policy.Modules, policy.ModuleSecrets)
}

func validateModuleSecrets(modules []protocol.ModuleConfigV1, secrets map[string]map[string]string) error {
	expected := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		moduleKey := agent.ModuleKey(module)
		if !module.Enabled {
			if _, exists := secrets[moduleKey]; exists {
				return fmt.Errorf("已停用模块 %q 不允许携带密钥", moduleKey)
			}
			continue
		}
		expected[moduleKey] = struct{}{}
		moduleSecrets, exists := secrets[moduleKey]
		if !exists {
			return fmt.Errorf("模块 %q 缺少密钥配置", moduleKey)
		}
		var allowed []string
		switch module.ID {
		case "filebrowser":
			allowed = []string{"api_token"}
		case "jellyfin":
			allowed = []string{"api_key"}
		case "cc-connect":
			allowed = []string{"bot_id", "bot_secret", "allow_from", "admin_from"}
		}
		if err := requireExactSecrets(moduleKey, moduleSecrets, allowed); err != nil {
			return err
		}
		if module.ID == "cc-connect" {
			if err := validateExplicitUsers("allow_from", moduleSecrets["allow_from"]); err != nil {
				return fmt.Errorf("cc-connect 项目 %q: %w", moduleKey, err)
			}
			if err := validateExplicitUsers("admin_from", moduleSecrets["admin_from"]); err != nil {
				return fmt.Errorf("cc-connect 项目 %q: %w", moduleKey, err)
			}
		}
	}
	for moduleKey := range secrets {
		if _, exists := expected[moduleKey]; !exists {
			return fmt.Errorf("存在未启用模块的密钥配置 %q", moduleKey)
		}
	}
	return nil
}

func requireExactSecrets(moduleKey string, values map[string]string, allowed []string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
		if strings.TrimSpace(values[key]) == "" {
			return fmt.Errorf("模块 %q 缺少必填密钥 %q", moduleKey, key)
		}
	}
	for key := range values {
		if _, exists := allowedSet[key]; !exists {
			return fmt.Errorf("模块 %q 包含不允许的密钥字段 %q", moduleKey, key)
		}
	}
	return nil
}

func validateExplicitUsers(field, value string) error {
	users := strings.Split(value, ",")
	if len(users) == 0 {
		return fmt.Errorf("%s 必须明确填写", field)
	}
	for _, user := range users {
		trimmed := strings.TrimSpace(user)
		if trimmed == "" || trimmed == "*" {
			return fmt.Errorf("%s 不允许为空或使用通配符", field)
		}
	}
	return nil
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

func ServeTLS(ctx context.Context, address, certFile, keyFile string, handler http.Handler) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
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
