package control

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

var (
	ErrDeviceNotFound = errors.New("设备不存在")
	ErrDeviceDenied   = errors.New("无权访问该设备")
)

type DeviceRecord struct {
	ID           string                        `json:"id"`
	Name         string                        `json:"name"`
	OwnerSubject string                        `json:"owner_subject"`
	OwnerEmail   string                        `json:"owner_email"`
	AgentURL     string                        `json:"agent_url"`
	TokenHash    string                        `json:"token_hash"`
	Config       protocol.SignedDeviceConfigV1 `json:"config"`
	SignedConfig string                        `json:"signed_config"`
	Status       protocol.DeviceStatusV1       `json:"status"`
	CreatedAt    time.Time                     `json:"created_at"`
}

type DeviceView struct {
	ID        string                        `json:"id"`
	Name      string                        `json:"name"`
	AgentURL  string                        `json:"agent_url"`
	Config    protocol.SignedDeviceConfigV1 `json:"config"`
	Status    protocol.DeviceStatusV1       `json:"status"`
	CreatedAt time.Time                     `json:"created_at"`
}

type DeviceStore struct {
	mu      sync.RWMutex
	path    string
	devices map[string]DeviceRecord
}

func OpenDeviceStore(path string) (*DeviceStore, error) {
	store := &DeviceStore{path: path, devices: map[string]DeviceRecord{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取设备状态失败: %w", err)
	}
	if err := json.Unmarshal(data, &store.devices); err != nil {
		return nil, fmt.Errorf("解析设备状态失败: %w", err)
	}
	return store, nil
}

func NewMemoryDeviceStore() *DeviceStore {
	return &DeviceStore{devices: map[string]DeviceRecord{}}
}

func (s *DeviceStore) Add(record DeviceRecord, rawToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.devices[record.ID]; exists {
		return errors.New("设备 ID 已存在")
	}
	record.TokenHash = hashToken(rawToken)
	s.devices[record.ID] = record
	if err := s.saveLocked(); err != nil {
		delete(s.devices, record.ID)
		return err
	}
	return nil
}

func (s *DeviceStore) Authenticate(deviceID, rawToken string) (DeviceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, exists := s.devices[deviceID]
	if !exists {
		return DeviceRecord{}, ErrDeviceNotFound
	}
	actual := hashToken(rawToken)
	if subtle.ConstantTimeCompare([]byte(actual), []byte(record.TokenHash)) != 1 {
		return DeviceRecord{}, ErrDeviceDenied
	}
	return record, nil
}

func (s *DeviceStore) UpdateStatus(deviceID string, status protocol.DeviceStatusV1) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.devices[deviceID]
	if !exists {
		return ErrDeviceNotFound
	}
	previous := record.Status
	record.Status = status
	s.devices[deviceID] = record
	if err := s.saveLocked(); err != nil {
		record.Status = previous
		s.devices[deviceID] = record
		return err
	}
	return nil
}

func (s *DeviceStore) RotateConfig(deviceID string, now time.Time, sign func(protocol.SignedDeviceConfigV1) (string, error)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.devices[deviceID]
	if !exists {
		return "", ErrDeviceNotFound
	}
	if record.Config.ExpiresAt.After(now.Add(time.Hour)) {
		return record.SignedConfig, nil
	}
	previous := record
	record.Config.Revision++
	record.Config.IssuedAt = now
	record.Config.ExpiresAt = now.Add(24 * time.Hour)
	signed, err := sign(record.Config)
	if err != nil {
		return "", err
	}
	record.SignedConfig = signed
	s.devices[deviceID] = record
	if err := s.saveLocked(); err != nil {
		s.devices[deviceID] = previous
		return "", err
	}
	return signed, nil
}

func (s *DeviceStore) Owned(deviceID, ownerSubject string) (DeviceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, exists := s.devices[deviceID]
	if !exists {
		return DeviceRecord{}, ErrDeviceNotFound
	}
	if record.OwnerSubject != ownerSubject {
		return DeviceRecord{}, ErrDeviceDenied
	}
	return record, nil
}

func (s *DeviceStore) List(ownerSubject string) []DeviceView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	views := make([]DeviceView, 0)
	for _, record := range s.devices {
		if record.OwnerSubject != ownerSubject {
			continue
		}
		views = append(views, DeviceView{
			ID: record.ID, Name: record.Name, AgentURL: record.AgentURL, Config: record.Config, Status: record.Status, CreatedAt: record.CreatedAt,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}

func (s *DeviceStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.devices, "", "  ")
	if err != nil {
		return fmt.Errorf("编码设备状态失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建设备状态目录失败: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".devices-*")
	if err != nil {
		return fmt.Errorf("创建设备状态临时文件失败: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置设备状态权限失败: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入设备状态失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步设备状态失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭设备状态失败: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("替换设备状态失败: %w", err)
	}
	return nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
