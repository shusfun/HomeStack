package setup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

type ServerOptions struct {
	TokenHashPath string
	SessionPath   string
	Helper        Helper
	Now           func() time.Time
	Random        io.Reader
}

type session struct {
	Hash      [32]byte
	ExpiresAt time.Time
}

type persistedSession struct {
	Hash      string    `json:"hash"`
	ExpiresAt time.Time `json:"expires_at"`
}

type attemptWindow struct {
	Started time.Time
	Count   int
}

type Server struct {
	tokenHash      [32]byte
	tokenPath      string
	sessionPath    string
	tokenAvailable bool
	helper         Helper
	now            func() time.Time
	random         io.Reader
	mu             sync.Mutex
	sessions       map[[32]byte]session
	attempts       map[string]attemptWindow
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Helper == nil || strings.TrimSpace(options.TokenHashPath) == "" {
		return nil, errors.New("Setup Server 配置不完整")
	}
	if options.SessionPath == "" {
		options.SessionPath = "/etc/homestack/setup-session.json"
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	server := &Server{tokenPath: options.TokenHashPath, sessionPath: options.SessionPath, helper: options.Helper, now: options.Now, random: options.Random, sessions: map[[32]byte]session{}, attempts: map[string]attemptWindow{}}
	if err := server.loadCredentialState(); err != nil {
		return nil, err
	}
	return server, nil
}

func (s *Server) loadCredentialState() error {
	if data, err := os.ReadFile(s.sessionPath); err == nil {
		var stored persistedSession
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&stored); err != nil {
			return fmt.Errorf("解析 Setup 会话摘要失败: %w", err)
		}
		decoded, err := hex.DecodeString(stored.Hash)
		if err != nil || len(decoded) != sha256.Size || stored.ExpiresAt.IsZero() {
			return errors.New("Setup 会话摘要无效")
		}
		var hash [32]byte
		copy(hash[:], decoded)
		s.sessions[hash] = session{Hash: hash, ExpiresAt: stored.ExpiresAt.UTC()}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取 Setup 会话摘要失败: %w", err)
	}
	data, err := os.ReadFile(s.tokenPath)
	if err != nil {
		return fmt.Errorf("读取 Setup 令牌摘要失败: %w", err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("Setup 令牌摘要必须是 SHA-256 十六进制")
	}
	copy(s.tokenHash[:], decoded)
	s.tokenAvailable = true
	return nil
}

func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/setup/status", s.status)
	mux.HandleFunc("POST /api/v1/setup/session", s.createSession)
	mux.HandleFunc("POST /api/v1/setup/prepare", s.prepare)
	mux.HandleFunc("POST /api/v1/setup/finalize", s.finalize)
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "setup"})
	})
	if static != nil {
		mux.Handle("/", static)
	}
	return setupHeaders(mux)
}

func (s *Server) status(writer http.ResponseWriter, request *http.Request) {
	status, err := s.helper.Status(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "helper_failed", err.Error())
		return
	}
	status.Surface = "setup"
	if status.Phase == PhaseCompleted || s.authenticated(request) {
		writeJSON(writer, http.StatusOK, status)
		return
	}
	writeJSON(writer, http.StatusOK, Status{Surface: "setup", Phase: PhaseToken, UpdatedAt: status.UpdatedAt})
}

func (s *Server) createSession(writer http.ResponseWriter, request *http.Request) {
	if !s.validProxyRequest(request) {
		writeError(writer, http.StatusForbidden, "proxy_rejected", "Setup 只接受本机 HTTPS 反向代理请求")
		return
	}
	client := clientAddress(request)
	if !s.allowAttempt(client) {
		writeError(writer, http.StatusTooManyRequests, "rate_limited", "Setup 令牌尝试次数过多")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.mu.Lock()
	if !s.tokenAvailable {
		s.mu.Unlock()
		writeError(writer, http.StatusLocked, "token_consumed", "Setup 一次性令牌已使用")
		return
	}
	digest := sha256.Sum256([]byte(body.Token))
	if subtle.ConstantTimeCompare(digest[:], s.tokenHash[:]) != 1 {
		s.mu.Unlock()
		writeError(writer, http.StatusUnauthorized, "token_rejected", "Setup 令牌无效")
		return
	}
	s.tokenAvailable = false
	s.mu.Unlock()
	restoreToken := func() {
		s.mu.Lock()
		s.tokenAvailable = true
		s.mu.Unlock()
	}
	raw, hashed, err := randomSession(s.random)
	if err != nil {
		restoreToken()
		writeError(writer, http.StatusInternalServerError, "random_failed", err.Error())
		return
	}
	expiresAt := s.now().UTC().Add(24 * time.Hour)
	if err := persistSession(s.sessionPath, persistedSession{Hash: hex.EncodeToString(hashed[:]), ExpiresAt: expiresAt}); err != nil {
		restoreToken()
		writeError(writer, http.StatusInternalServerError, "session_persist_failed", err.Error())
		return
	}
	if err := os.Remove(s.tokenPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(s.sessionPath)
		restoreToken()
		writeError(writer, http.StatusInternalServerError, "token_consume_failed", fmt.Sprintf("删除 Setup 令牌摘要失败: %v", err))
		return
	}
	s.mu.Lock()
	s.sessions[hashed] = session{Hash: hashed, ExpiresAt: expiresAt}
	s.tokenHash = [32]byte{}
	delete(s.attempts, client)
	s.mu.Unlock()
	http.SetCookie(writer, &http.Cookie{Name: SessionCookieName, Value: raw, Path: "/", Expires: expiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writer.WriteHeader(http.StatusNoContent)
}

func persistSession(path string, value persistedSession) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".setup-session-*")
	if err != nil {
		return fmt.Errorf("创建 Setup 会话摘要失败: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("保存 Setup 会话摘要失败: %w", err)
	}
	return nil
}

func (s *Server) prepare(writer http.ResponseWriter, request *http.Request) {
	if !s.authorizeWrite(writer, request) {
		return
	}
	var config Configuration
	if err := decodeJSON(writer, request, &config); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	config = normalizeConfiguration(config)
	if err := ValidateConfiguration(config); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_configuration", err.Error())
		return
	}
	if err := validateDNS(request.Context(), config); err != nil {
		writeError(writer, http.StatusBadRequest, "dns_rejected", err.Error())
		return
	}
	status, err := s.helper.Prepare(request.Context(), config)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "prepare_failed", err.Error())
		return
	}
	status.Surface = "setup"
	writeJSON(writer, http.StatusAccepted, status)
}

func (s *Server) finalize(writer http.ResponseWriter, request *http.Request) {
	if !s.authorizeWrite(writer, request) {
		return
	}
	status, err := s.helper.Finalize(request.Context())
	if err != nil {
		writeError(writer, http.StatusBadGateway, "finalize_failed", err.Error())
		return
	}
	status.Surface = "setup"
	writeJSON(writer, http.StatusAccepted, status)
}

func (s *Server) authorizeWrite(writer http.ResponseWriter, request *http.Request) bool {
	if !s.validProxyRequest(request) || !s.authenticated(request) {
		writeError(writer, http.StatusUnauthorized, "setup_unauthenticated", "Setup 会话无效或已过期")
		return false
	}
	origin := strings.TrimRight(request.Header.Get("Origin"), "/")
	expected := "https://" + request.Host
	if origin == "" || origin != expected {
		writeError(writer, http.StatusForbidden, "origin_rejected", "请求来源与 Setup 地址不匹配")
		return false
	}
	return true
}

func (s *Server) authenticated(request *http.Request) bool {
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.sessions[digest]
	if !exists || !s.now().UTC().Before(entry.ExpiresAt) {
		delete(s.sessions, digest)
		return false
	}
	return true
}

func (s *Server) validProxyRequest(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	return request.Header.Get("X-Forwarded-Proto") == "https" && strings.TrimSpace(request.Host) != ""
}

func (s *Server) allowAttempt(client string) bool {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.attempts[client]
	if entry.Started.IsZero() || now.Sub(entry.Started) >= time.Minute {
		entry = attemptWindow{Started: now}
	}
	entry.Count++
	s.attempts[client] = entry
	return entry.Count <= 5
}

func validateDNS(ctx context.Context, config Configuration) error {
	expected := net.ParseIP(config.PublicIPv4).To4()
	resolver := net.DefaultResolver
	for _, host := range []string{config.ControlHost, config.PocketHost, config.MeshHost} {
		addresses, err := resolver.LookupIP(ctx, "ip4", host)
		if err != nil {
			return fmt.Errorf("解析域名 %s 失败: %w", host, err)
		}
		matched := false
		for _, address := range addresses {
			if address.To4() != nil && address.To4().Equal(expected) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("域名 %s 未直接解析到 VPS 公网 IPv4 %s", host, config.PublicIPv4)
		}
	}
	return nil
}

func normalizeConfiguration(config Configuration) Configuration {
	config.ControlHost = strings.ToLower(strings.TrimSpace(config.ControlHost))
	config.PocketHost = strings.ToLower(strings.TrimSpace(config.PocketHost))
	config.MeshHost = strings.ToLower(strings.TrimSpace(config.MeshHost))
	config.TailHost = strings.ToLower(strings.TrimSpace(config.TailHost))
	config.PublicIPv4 = strings.TrimSpace(config.PublicIPv4)
	return config
}

func clientAddress(request *http.Request) string {
	if value := net.ParseIP(strings.TrimSpace(request.Header.Get("X-Real-IP"))); value != nil {
		return value.String()
	}
	return request.RemoteAddr
}

func randomSession(reader io.Reader) (string, [32]byte, error) {
	data := make([]byte, 32)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", [32]byte{}, fmt.Errorf("生成 Setup 会话失败: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(data)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, 32<<10)
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

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, protocol.ErrorResponse{Error: protocol.APIError{Code: code, Message: message}})
}

func setupHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		next.ServeHTTP(writer, request)
	})
}
