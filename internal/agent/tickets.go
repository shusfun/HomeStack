package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
)

var ErrTicketReplay = errors.New("访问票据已使用")

type SessionStore struct {
	mu        sync.Mutex
	path      string
	deviceID  string
	issuer    string
	publicKey ed25519.PublicKey
	keyID     string
	now       func() time.Time
	random    io.Reader
	used      map[string]time.Time
	sessions  map[string]time.Time
}

func OpenSessionStore(path, deviceID, issuer string, publicKey ed25519.PublicKey, keyID string) (*SessionStore, error) {
	store := &SessionStore{
		path: path, deviceID: deviceID, issuer: issuer, publicKey: publicKey, keyID: keyID,
		now: time.Now, random: rand.Reader, used: map[string]time.Time{}, sessions: map[string]time.Time{},
	}
	if path == "" {
		return store, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取票据防重放状态失败: %w", err)
	}
	if err := json.Unmarshal(data, &store.used); err != nil {
		return nil, fmt.Errorf("解析票据防重放状态失败: %w", err)
	}
	store.pruneLocked(store.now().UTC())
	return store, nil
}

func (s *SessionStore) Redeem(signed string) (string, error) {
	var claims protocol.AccessTicketClaims
	if err := secure.VerifyJWS(signed, s.publicKey, s.keyID, &claims); err != nil {
		return "", fmt.Errorf("验证访问票据失败: %w", err)
	}
	now := s.now().UTC()
	if claims.DeviceID != s.deviceID || claims.Issuer != s.issuer {
		return "", errors.New("访问票据与当前设备不匹配")
	}
	if claims.Nonce == "" || claims.Subject == "" || !now.Before(claims.ExpiresAt) || claims.IssuedAt.After(now.Add(30*time.Second)) {
		return "", errors.New("访问票据无效或已过期")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if _, exists := s.used[claims.Nonce]; exists {
		return "", ErrTicketReplay
	}
	sessionBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.random, sessionBytes); err != nil {
		return "", fmt.Errorf("生成 Agent 会话失败: %w", err)
	}
	session := base64.RawURLEncoding.EncodeToString(sessionBytes)
	s.used[claims.Nonce] = claims.ExpiresAt
	s.sessions[session] = now.Add(8 * time.Hour)
	if err := s.saveLocked(); err != nil {
		delete(s.used, claims.Nonce)
		delete(s.sessions, session)
		return "", err
	}
	return session, nil
}

func (s *SessionStore) Valid(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt, exists := s.sessions[session]
	if !exists {
		return false
	}
	if !s.now().UTC().Before(expiresAt) {
		delete(s.sessions, session)
		return false
	}
	return true
}

func (s *SessionStore) pruneLocked(now time.Time) {
	for nonce, expiresAt := range s.used {
		if !now.Before(expiresAt) {
			delete(s.used, nonce)
		}
	}
	for session, expiresAt := range s.sessions {
		if !now.Before(expiresAt) {
			delete(s.sessions, session)
		}
	}
}

func (s *SessionStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.used, "", "  ")
	if err != nil {
		return fmt.Errorf("编码票据防重放状态失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建票据状态目录失败: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".tickets-*")
	if err != nil {
		return fmt.Errorf("创建票据状态临时文件失败: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置票据状态权限失败: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入票据状态失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步票据状态失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭票据状态失败: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("替换票据状态失败: %w", err)
	}
	return nil
}
