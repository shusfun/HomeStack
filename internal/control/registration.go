package control

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/agent"
	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
)

var (
	ErrActivationRejected = errors.New("激活码无效、已使用或已过期")
	ErrActivationExists   = errors.New("该 Owner 已有未过期激活码")
)

type activationRecord struct {
	OwnerID   string    `json:"owner_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ActivationStore struct {
	mu      sync.Mutex
	path    string
	records map[string]activationRecord
	now     func() time.Time
	random  io.Reader
}

func OpenActivationStore(path string, now func() time.Time, random io.Reader) (*ActivationStore, error) {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Reader
	}
	store := &ActivationStore{path: path, records: map[string]activationRecord{}, now: now, random: random}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || path == "" {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取设备激活码失败: %w", err)
	}
	if err := json.Unmarshal(data, &store.records); err != nil {
		return nil, fmt.Errorf("解析设备激活码失败: %w", err)
	}
	return store, nil
}

func (s *ActivationStore) Create(ownerID string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	for _, record := range s.records {
		if record.OwnerID == ownerID {
			return "", time.Time{}, ErrActivationExists
		}
	}
	raw, err := randomToken(s.random, 32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := s.now().UTC().Add(10 * time.Minute)
	s.records[hashToken(raw)] = activationRecord{OwnerID: ownerID, ExpiresAt: expiresAt}
	if err := s.saveLocked(); err != nil {
		delete(s.records, hashToken(raw))
		return "", time.Time{}, err
	}
	return raw, expiresAt, nil
}

func (s *ActivationStore) Redeem(raw string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[hashToken(raw)]
	if !ok || !s.now().UTC().Before(record.ExpiresAt) {
		return "", ErrActivationRejected
	}
	delete(s.records, hashToken(raw))
	if err := s.saveLocked(); err != nil {
		s.records[hashToken(raw)] = record
		return "", err
	}
	return record.OwnerID, nil
}

func (s *ActivationStore) pruneLocked() {
	now := s.now().UTC()
	for digest, record := range s.records {
		if !now.Before(record.ExpiresAt) {
			delete(s.records, digest)
		}
	}
}

func (s *ActivationStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".activations-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
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
	return os.Rename(temporary, s.path)
}

type RegistrationService struct {
	devices      *DeviceStore
	signingKey   ed25519.PrivateKey
	signingKeyID string
	publicURL    string
	now          func() time.Time
	random       io.Reader
}

func NewRegistrationService(devices *DeviceStore, signingKey ed25519.PrivateKey, signingKeyID, publicURL string, now func() time.Time, random io.Reader) (*RegistrationService, error) {
	if devices == nil || len(signingKey) != ed25519.PrivateKeySize || signingKeyID == "" || publicURL == "" {
		return nil, errors.New("设备登记服务配置不完整")
	}
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Reader
	}
	return &RegistrationService{devices: devices, signingKey: signingKey, signingKeyID: signingKeyID, publicURL: strings.TrimRight(publicURL, "/"), now: now, random: random}, nil
}

func (s *RegistrationService) Register(ownerID string, request protocol.NodeRegistration) (protocol.RegistrationResponse, error) {
	request.MagicDNS = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(request.MagicDNS)), ".")
	request.Name = strings.TrimSpace(request.Name)
	if err := validateNodeRegistration(request); err != nil {
		return protocol.RegistrationResponse{}, err
	}
	publicBytes, err := base64.RawURLEncoding.DecodeString(request.EncryptionPublicKey)
	if err != nil {
		return protocol.RegistrationResponse{}, errors.New("临时 X25519 公钥编码无效")
	}
	publicKey, err := ecdh.X25519().NewPublicKey(publicBytes)
	if err != nil {
		return protocol.RegistrationResponse{}, errors.New("临时 X25519 公钥无效")
	}
	deviceID := ""
	createdAt := s.now().UTC()
	revision := uint64(1)
	if existing, ok := s.devices.ExistingByKey(ownerID, request.DevicePublicKey); ok {
		deviceID, createdAt = existing.ID, existing.CreatedAt
		revision = existing.Config.Revision + 1
	} else if deviceID, err = randomToken(s.random, 16); err != nil {
		return protocol.RegistrationResponse{}, err
	}
	deviceToken, err := randomToken(s.random, 32)
	if err != nil {
		return protocol.RegistrationResponse{}, err
	}
	now := s.now().UTC()
	agentURL := "https://" + request.MagicDNS + ":19443"
	config := protocol.SignedDeviceConfig{DeviceID: deviceID, DeviceName: request.Name, Revision: revision, IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour), ControlURL: s.publicURL, AgentURL: agentURL, Modules: request.Modules, SharedDirectories: request.SharedDirectories}
	if err := agent.ValidateDeviceConfig(config); err != nil {
		return protocol.RegistrationResponse{}, err
	}
	signed, err := secure.SignJWS(s.signingKey, s.signingKeyID, config)
	if err != nil {
		return protocol.RegistrationResponse{}, err
	}
	credential := protocol.DeviceCredential{DeviceID: deviceID, DeviceToken: deviceToken, ExpiresAt: now.Add(10 * time.Minute)}
	sealed, err := secure.SealJSON(publicKey, credential)
	if err != nil {
		return protocol.RegistrationResponse{}, err
	}
	suffix := strings.SplitN(request.MagicDNS, ".", 2)[1]
	record := DeviceRecord{ID: deviceID, Name: request.Name, OwnerSubject: ownerID, Platform: request.Platform, Architecture: request.Architecture, TailscaleIP: request.TailscaleIP, MagicDNS: request.MagicDNS, DevicePublicKey: request.DevicePublicKey, AgentURL: agentURL, Config: config, SignedConfig: signed, CreatedAt: createdAt}
	stored, err := s.devices.Register(record, deviceToken, suffix)
	if err != nil {
		return protocol.RegistrationResponse{}, err
	}
	return protocol.RegistrationResponse{DeviceID: stored.ID, DeviceName: stored.Name, SealedCredential: sealed, SignedConfig: signed}, nil
}

func validateNodeRegistration(request protocol.NodeRegistration) error {
	if request.Name == "" || len(request.Name) > 80 || strings.ContainsAny(request.Name, "\r\n\x00") {
		return errors.New("设备名称无效")
	}
	if request.Platform != "darwin" && request.Platform != "windows" && request.Platform != "linux" {
		return errors.New("设备平台不受支持")
	}
	if request.Architecture != "amd64" && request.Architecture != "arm64" {
		return errors.New("设备架构不受支持")
	}
	ip, err := netip.ParseAddr(request.TailscaleIP)
	if err != nil || (!netip.MustParsePrefix("100.64.0.0/10").Contains(ip) && !netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip)) {
		return errors.New("设备地址不是有效的 Tailscale IP")
	}
	labels := strings.Split(request.MagicDNS, ".")
	if len(labels) < 3 || labels[0] == "" || !strings.HasSuffix(request.MagicDNS, ".ts.net") || strings.ContainsAny(request.MagicDNS, "/:@?#[]") {
		return errors.New("设备 MagicDNS 名称无效")
	}
	deviceKey, err := base64.RawURLEncoding.DecodeString(request.DevicePublicKey)
	if err != nil || len(deviceKey) != ed25519.PublicKeySize {
		return errors.New("设备公钥无效")
	}
	seen := map[string]bool{}
	for _, directory := range request.SharedDirectories {
		if directory.ID == "" || directory.Name == "" || !isCanonicalAbsolutePath(request.Platform, directory.Path) || seen[directory.ID] {
			return errors.New("共享目录必须使用唯一 ID、名称和规范化绝对路径")
		}
		seen[directory.ID] = true
	}
	return nil
}

func isCanonicalAbsolutePath(platform, value string) bool {
	switch platform {
	case "darwin", "linux":
		return path.IsAbs(value) && path.Clean(value) == value
	case "windows":
		return isCanonicalWindowsAbsolutePath(value)
	default:
		return false
	}
}

func isCanonicalWindowsAbsolutePath(value string) bool {
	if value == "" || strings.ContainsAny(value, "/\x00") {
		return false
	}
	if len(value) >= 3 && isASCIILetter(value[0]) && value[1] == ':' && value[2] == '\\' {
		return len(value) == 3 || hasCanonicalWindowsComponents(value[3:])
	}
	if !strings.HasPrefix(value, `\\`) {
		return false
	}
	components := strings.Split(value[2:], `\`)
	if len(components) < 2 {
		return false
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." || strings.ContainsRune(component, ':') {
			return false
		}
	}
	return true
}

func hasCanonicalWindowsComponents(value string) bool {
	for _, component := range strings.Split(value, `\`) {
		if component == "" || component == "." || component == ".." || strings.ContainsRune(component, ':') {
			return false
		}
	}
	return true
}

func isASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
