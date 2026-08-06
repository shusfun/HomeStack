package invite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

var (
	ErrNotFound = errors.New("一次性连接码不存在")
	ErrExpired  = errors.New("一次性连接码已过期")
	ErrUsed     = errors.New("一次性连接码已使用")
)

type Record struct {
	ID        string          `json:"id"`
	CodeHash  string          `json:"code_hash"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`
	UsedAt    *time.Time      `json:"used_at,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type Store struct {
	mu      sync.Mutex
	path    string
	now     func() time.Time
	random  io.Reader
	records map[string]Record
}

func Open(path string) (*Store, error) {
	store := &Store{path: path, now: time.Now, random: rand.Reader, records: map[string]Record{}}
	if path == "" {
		return store, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取邀请码状态失败: %w", err)
	}
	if err := json.Unmarshal(data, &store.records); err != nil {
		return nil, fmt.Errorf("解析邀请码状态失败: %w", err)
	}
	return store, nil
}

func NewMemory(now func() time.Time, random io.Reader) *Store {
	return &Store{now: now, random: random, records: map[string]Record{}}
}

func (s *Store) Create(server, createdBy string, ttl time.Duration, payload json.RawMessage) (protocol.JoinDescriptorV1, Record, error) {
	if ttl <= 0 {
		return protocol.JoinDescriptorV1{}, Record{}, errors.New("邀请码有效期必须大于零")
	}
	codeBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.random, codeBytes); err != nil {
		return protocol.JoinDescriptorV1{}, Record{}, fmt.Errorf("生成一次性连接码失败: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(codeBytes)
	descriptor, err := protocol.NewJoinDescriptor(server, code)
	if err != nil {
		return protocol.JoinDescriptorV1{}, Record{}, err
	}
	now := s.now().UTC()
	hash := hashCode(code)
	record := Record{
		ID:        base64.RawURLEncoding.EncodeToString(codeBytes[:12]),
		CodeHash:  hash,
		CreatedBy: createdBy,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		Payload:   append(json.RawMessage(nil), payload...),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[hash] = record
	if err := s.saveLocked(); err != nil {
		delete(s.records, hash)
		return protocol.JoinDescriptorV1{}, Record{}, err
	}
	return descriptor, record, nil
}

func (s *Store) Redeem(code string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := hashCode(code)
	record, ok := s.records[hash]
	if !ok {
		return Record{}, ErrNotFound
	}
	if record.UsedAt != nil {
		return Record{}, ErrUsed
	}
	now := s.now().UTC()
	if !now.Before(record.ExpiresAt) {
		return Record{}, ErrExpired
	}
	previous := record
	redeemed := record
	record.UsedAt = &now
	record.Payload = nil
	s.records[hash] = record
	if err := s.saveLocked(); err != nil {
		s.records[hash] = previous
		return Record{}, err
	}
	redeemed.UsedAt = &now
	return redeemed, nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return fmt.Errorf("编码邀请码状态失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建邀请码状态目录失败: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".invites-*")
	if err != nil {
		return fmt.Errorf("创建邀请码临时文件失败: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("设置邀请码状态权限失败: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("写入邀请码状态失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("同步邀请码状态失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("关闭邀请码状态文件失败: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("替换邀请码状态失败: %w", err)
	}
	return nil
}

func hashCode(code string) string {
	digest := sha256.Sum256([]byte(code))
	return hex.EncodeToString(digest[:])
}
