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
	ErrTailnetChanged = errors.New("设备不属于 Owner 已锁定的 Tailnet")
)

type DeviceRecord struct {
	ID              string                      `json:"id"`
	Name            string                      `json:"name"`
	OwnerSubject    string                      `json:"owner_subject"`
	Platform        string                      `json:"platform"`
	Architecture    string                      `json:"architecture"`
	TailscaleIP     string                      `json:"tailscale_ip"`
	MagicDNS        string                      `json:"magic_dns"`
	DevicePublicKey string                      `json:"device_public_key"`
	AgentURL        string                      `json:"agent_url"`
	TokenHash       string                      `json:"token_hash"`
	Config          protocol.SignedDeviceConfig `json:"config"`
	SignedConfig    string                      `json:"signed_config"`
	Status          protocol.DeviceStatus       `json:"status"`
	CreatedAt       time.Time                   `json:"created_at"`
}

type DeviceView struct {
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Platform     string                      `json:"platform"`
	Architecture string                      `json:"architecture"`
	TailscaleIP  string                      `json:"tailscale_ip"`
	MagicDNS     string                      `json:"magic_dns"`
	AgentURL     string                      `json:"agent_url"`
	Config       protocol.SignedDeviceConfig `json:"config"`
	Status       protocol.DeviceStatus       `json:"status"`
	CreatedAt    time.Time                   `json:"created_at"`
}

type persistedDevices struct {
	TailnetSuffix string                  `json:"tailnet_suffix"`
	Devices       map[string]DeviceRecord `json:"devices"`
}

type DeviceStore struct {
	mu            sync.RWMutex
	path          string
	tailnetSuffix string
	devices       map[string]DeviceRecord
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
	var persisted persistedDevices
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, fmt.Errorf("解析设备状态失败: %w", err)
	}
	if persisted.Devices == nil {
		return nil, errors.New("设备状态缺少 devices")
	}
	store.tailnetSuffix, store.devices = persisted.TailnetSuffix, persisted.Devices
	return store, nil
}

func NewMemoryDeviceStore() *DeviceStore { return &DeviceStore{devices: map[string]DeviceRecord{}} }

func (s *DeviceStore) Register(record DeviceRecord, rawToken, tailnetSuffix string) (DeviceRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tailnetSuffix != "" && s.tailnetSuffix != tailnetSuffix {
		return DeviceRecord{}, ErrTailnetChanged
	}
	previousSuffix := s.tailnetSuffix
	if s.tailnetSuffix == "" {
		s.tailnetSuffix = tailnetSuffix
	}
	for id, existing := range s.devices {
		if existing.DevicePublicKey != record.DevicePublicKey {
			continue
		}
		if existing.OwnerSubject != record.OwnerSubject {
			return DeviceRecord{}, ErrDeviceDenied
		}
		record.ID, record.CreatedAt = id, existing.CreatedAt
		break
	}
	previous, existed := s.devices[record.ID]
	record.TokenHash = hashToken(rawToken)
	s.devices[record.ID] = record
	if err := s.saveLocked(); err != nil {
		if existed {
			s.devices[record.ID] = previous
		} else {
			delete(s.devices, record.ID)
		}
		s.tailnetSuffix = previousSuffix
		return DeviceRecord{}, err
	}
	return record, nil
}

func (s *DeviceStore) Authenticate(deviceID, rawToken string) (DeviceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, exists := s.devices[deviceID]
	if !exists {
		return DeviceRecord{}, ErrDeviceNotFound
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(rawToken)), []byte(record.TokenHash)) != 1 {
		return DeviceRecord{}, ErrDeviceDenied
	}
	return record, nil
}

func (s *DeviceStore) UpdateStatus(deviceID string, status protocol.DeviceStatus) error {
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

func (s *DeviceStore) RotateConfig(deviceID string, now time.Time, sign func(protocol.SignedDeviceConfig) (string, error)) (string, error) {
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
	record.Config.IssuedAt, record.Config.ExpiresAt = now, now.Add(24*time.Hour)
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
	views := make([]DeviceView, 0, len(s.devices))
	for _, record := range s.devices {
		if record.OwnerSubject != ownerSubject {
			continue
		}
		views = append(views, DeviceView{ID: record.ID, Name: record.Name, Platform: record.Platform, Architecture: record.Architecture, TailscaleIP: record.TailscaleIP, MagicDNS: record.MagicDNS, AgentURL: record.AgentURL, Config: record.Config, Status: record.Status, CreatedAt: record.CreatedAt})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}

func (s *DeviceStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.devices)
}

func (s *DeviceStore) Remove(deviceID, ownerSubject string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.devices[deviceID]
	if !exists {
		return ErrDeviceNotFound
	}
	if record.OwnerSubject != ownerSubject {
		return ErrDeviceDenied
	}
	delete(s.devices, deviceID)
	previousSuffix := s.tailnetSuffix
	if len(s.devices) == 0 {
		s.tailnetSuffix = ""
	}
	if err := s.saveLocked(); err != nil {
		s.devices[deviceID] = record
		s.tailnetSuffix = previousSuffix
		return err
	}
	return nil
}

func (s *DeviceStore) ExistingByKey(ownerSubject, publicKey string) (DeviceRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.devices {
		if record.OwnerSubject == ownerSubject && record.DevicePublicKey == publicKey {
			return record, true
		}
	}
	return DeviceRecord{}, false
}

func (s *DeviceStore) UpdateControlURL(publicURL string, now time.Time, sign func(protocol.SignedDeviceConfig) (string, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := make(map[string]DeviceRecord, len(s.devices))
	for id, record := range s.devices {
		previous[id] = record
	}
	for id, record := range s.devices {
		record.Config.ControlURL = publicURL
		record.Config.Revision++
		record.Config.IssuedAt = now
		record.Config.ExpiresAt = now.Add(24 * time.Hour)
		signed, err := sign(record.Config)
		if err != nil {
			s.devices = previous
			return err
		}
		record.SignedConfig = signed
		s.devices[id] = record
	}
	if err := s.saveLocked(); err != nil {
		s.devices = previous
		return err
	}
	return nil
}

func (s *DeviceStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(persistedDevices{TailnetSuffix: s.tailnetSuffix, Devices: s.devices}, "", "  ")
	if err != nil {
		return fmt.Errorf("编码设备状态失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".devices-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
