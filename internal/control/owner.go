package control

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrIdentityNotLinked = errors.New("该登录方式尚未绑定到 HomeStack 所有者")

type IdentityKey struct {
	Provider string `json:"provider"`
	Subject  string `json:"subject"`
}

type Owner struct {
	ID         string        `json:"id"`
	Email      string        `json:"email"`
	Name       string        `json:"name"`
	Identities []IdentityKey `json:"identities"`
	CreatedAt  time.Time     `json:"created_at"`
}

type authSession struct {
	OwnerID   string    `json:"owner_id"`
	Kind      string    `json:"kind"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ownerState struct {
	Owner    *Owner                 `json:"owner,omitempty"`
	Sessions map[string]authSession `json:"sessions"`
}

type OwnerStore struct {
	mu    sync.Mutex
	path  string
	now   func() time.Time
	state ownerState
}

func OpenOwnerStore(path string) (*OwnerStore, error) {
	store := &OwnerStore{path: path, now: time.Now, state: ownerState{Sessions: map[string]authSession{}}}
	if path == "" {
		return store, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取所有者状态失败: %w", err)
	}
	if err := json.Unmarshal(data, &store.state); err != nil {
		return nil, fmt.Errorf("解析所有者状态失败: %w", err)
	}
	if store.state.Sessions == nil {
		store.state.Sessions = map[string]authSession{}
	}
	if store.state.Owner != nil && (store.state.Owner.ID == "" || len(store.state.Owner.Identities) == 0) {
		return nil, errors.New("所有者状态不完整")
	}
	return store, nil
}

func (s *OwnerStore) AuthenticateOrClaim(external ExternalIdentity) (Identity, error) {
	if err := validateExternalIdentity(external); err != nil {
		return Identity{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := IdentityKey{Provider: external.Provider, Subject: external.Subject}
	if s.state.Owner == nil {
		ownerID, err := randomStoreToken(18)
		if err != nil {
			return Identity{}, err
		}
		now := s.now().UTC()
		s.state.Owner = &Owner{ID: ownerID, Email: external.Email, Name: external.Name, Identities: []IdentityKey{key}, CreatedAt: now}
		if err := s.saveLocked(); err != nil {
			s.state.Owner = nil
			return Identity{}, err
		}
		return identityForOwner(*s.state.Owner, external), nil
	}
	if !containsIdentity(s.state.Owner.Identities, key) {
		return Identity{}, ErrIdentityNotLinked
	}
	return identityForOwner(*s.state.Owner, external), nil
}

func (s *OwnerStore) ReplaceIdentity(ownerID string, external ExternalIdentity) (Owner, error) {
	if err := validateExternalIdentity(external); err != nil {
		return Owner{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Owner == nil || s.state.Owner.ID != ownerID {
		return Owner{}, ErrUnauthenticated
	}
	previous := *s.state.Owner
	previous.Identities = append([]IdentityKey(nil), s.state.Owner.Identities...)
	s.state.Owner.Identities = []IdentityKey{{Provider: external.Provider, Subject: external.Subject}}
	s.state.Owner.Email, s.state.Owner.Name = external.Email, external.Name
	if err := s.saveLocked(); err != nil {
		*s.state.Owner = previous
		return Owner{}, err
	}
	return previous, nil
}

func (s *OwnerStore) RestoreOwner(owner Owner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Owner == nil || s.state.Owner.ID != owner.ID {
		return ErrUnauthenticated
	}
	current := *s.state.Owner
	restored := owner
	restored.Identities = append([]IdentityKey(nil), owner.Identities...)
	s.state.Owner = &restored
	if err := s.saveLocked(); err != nil {
		s.state.Owner = &current
		return err
	}
	return nil
}

func (s *OwnerStore) CreateSession(ownerID, kind string, ttl time.Duration) (string, time.Time, error) {
	if kind != "browser" && kind != "access" && kind != "refresh" {
		return "", time.Time{}, errors.New("会话类型无效")
	}
	if ttl <= 0 {
		return "", time.Time{}, errors.New("会话有效期必须大于零")
	}
	raw, err := randomStoreToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Owner == nil || s.state.Owner.ID != ownerID {
		return "", time.Time{}, ErrUnauthenticated
	}
	now := s.now().UTC()
	s.pruneLocked(now)
	expiresAt := now.Add(ttl)
	s.state.Sessions[hashAuthToken(raw)] = authSession{OwnerID: ownerID, Kind: kind, ExpiresAt: expiresAt}
	if err := s.saveLocked(); err != nil {
		delete(s.state.Sessions, hashAuthToken(raw))
		return "", time.Time{}, err
	}
	return raw, expiresAt, nil
}

func (s *OwnerStore) ResolveSession(raw, kind string) (Owner, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.state.Sessions[hashAuthToken(raw)]
	if !ok || session.Kind != kind || !s.now().UTC().Before(session.ExpiresAt) || s.state.Owner == nil || session.OwnerID != s.state.Owner.ID {
		return Owner{}, false
	}
	return *s.state.Owner, true
}

func (s *OwnerStore) RevokeSession(raw string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state.Sessions, hashAuthToken(raw))
	return s.saveLocked()
}

func (s *OwnerStore) RevokeAllSessions() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.state.Sessions
	s.state.Sessions = map[string]authSession{}
	if err := s.saveLocked(); err != nil {
		s.state.Sessions = previous
		return err
	}
	return nil
}

func (s *OwnerStore) Owner() (Owner, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Owner == nil {
		return Owner{}, false
	}
	return *s.state.Owner, true
}

func (s *OwnerStore) pruneLocked(now time.Time) {
	for key, session := range s.state.Sessions {
		if !now.Before(session.ExpiresAt) {
			delete(s.state.Sessions, key)
		}
	}
}

func (s *OwnerStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("编码所有者状态失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建所有者状态目录失败: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".owner-*")
	if err != nil {
		return fmt.Errorf("创建所有者状态临时文件失败: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置所有者状态权限失败: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入所有者状态失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步所有者状态失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭所有者状态失败: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("替换所有者状态失败: %w", err)
	}
	return nil
}

func validateExternalIdentity(identity ExternalIdentity) error {
	if identity.Provider == "" || identity.Subject == "" || identity.Email == "" || !identity.EmailVerified {
		return errors.New("外部身份必须包含 provider、subject 和已验证邮箱")
	}
	return nil
}

func containsIdentity(values []IdentityKey, expected IdentityKey) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func identityForOwner(owner Owner, external ExternalIdentity) Identity {
	return Identity{Subject: owner.ID, Email: external.Email, Name: external.Name, Provider: external.Provider, ProviderSubject: external.Subject}
}

func randomStoreToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("生成安全随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func hashAuthToken(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}
