package agent

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
)

var ErrConfigRollback = errors.New("配置 revision 未递增")

var moduleInstancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type ConfigStore struct {
	mu        sync.RWMutex
	path      string
	deviceID  string
	publicKey ed25519.PublicKey
	keyID     string
	now       func() time.Time
	current   protocol.SignedDeviceConfigV1
	signed    string
}

type storedConfig struct {
	Signed string `json:"signed"`
}

func OpenConfigStore(path, deviceID string, publicKey ed25519.PublicKey, keyID string) (*ConfigStore, error) {
	store := &ConfigStore{path: path, deviceID: deviceID, publicKey: publicKey, keyID: keyID, now: time.Now}
	if path == "" {
		return store, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取设备配置失败: %w", err)
	}
	var persisted storedConfig
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, fmt.Errorf("解析设备配置文件失败: %w", err)
	}
	config, err := store.verifyToken(persisted.Signed, true)
	if err != nil {
		return nil, err
	}
	store.current = config
	store.signed = persisted.Signed
	return store, nil
}

func (s *ConfigStore) Apply(signed string) (protocol.SignedDeviceConfigV1, error) {
	config, err := s.verify(signed)
	if err != nil {
		return protocol.SignedDeviceConfigV1{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.Revision != 0 && config.Revision <= s.current.Revision {
		return protocol.SignedDeviceConfigV1{}, ErrConfigRollback
	}
	previousConfig, previousSigned := s.current, s.signed
	s.current, s.signed = config, signed
	if err := s.saveLocked(); err != nil {
		s.current, s.signed = previousConfig, previousSigned
		return protocol.SignedDeviceConfigV1{}, err
	}
	return config, nil
}

func (s *ConfigStore) Current() (protocol.SignedDeviceConfigV1, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current, s.current.Revision != 0
}

func (s *ConfigStore) Signed() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.signed
}

func (s *ConfigStore) verify(signed string) (protocol.SignedDeviceConfigV1, error) {
	return s.verifyToken(signed, false)
}

func (s *ConfigStore) verifyToken(signed string, allowExpired bool) (protocol.SignedDeviceConfigV1, error) {
	var config protocol.SignedDeviceConfigV1
	if err := secure.VerifyJWS(signed, s.publicKey, s.keyID, &config); err != nil {
		return protocol.SignedDeviceConfigV1{}, fmt.Errorf("验证设备配置失败: %w", err)
	}
	if config.Version != protocol.DeviceConfigVersion {
		return protocol.SignedDeviceConfigV1{}, errors.New("设备配置版本不受支持")
	}
	if config.DeviceID != s.deviceID {
		return protocol.SignedDeviceConfigV1{}, errors.New("设备配置与当前设备不匹配")
	}
	now := s.now().UTC()
	if config.IssuedAt.After(now.Add(2 * time.Minute)) {
		return protocol.SignedDeviceConfigV1{}, errors.New("设备配置签发时间晚于本机时间")
	}
	if !allowExpired && !now.Before(config.ExpiresAt) {
		return protocol.SignedDeviceConfigV1{}, errors.New("设备配置已过期")
	}
	if err := ValidateDeviceConfig(config); err != nil {
		return protocol.SignedDeviceConfigV1{}, err
	}
	return config, nil
}

func ValidateDeviceConfig(config protocol.SignedDeviceConfigV1) error {
	if config.Revision == 0 {
		return errors.New("设备配置 revision 必须大于零")
	}
	if config.DeviceID == "" || config.DeviceName == "" {
		return errors.New("设备配置必须包含设备 ID 和名称")
	}
	if !config.IssuedAt.Before(config.ExpiresAt) || config.ExpiresAt.Sub(config.IssuedAt) > 48*time.Hour {
		return errors.New("设备配置有效期无效或超过 48 小时")
	}
	if err := requireHTTPSURL(config.ControlURL, "Control", ""); err != nil {
		return err
	}
	if err := requireHTTPSURL(config.AgentURL, "Agent", "9443"); err != nil {
		return err
	}
	seenModules := map[string]struct{}{}
	for _, module := range config.Modules {
		if !slices.Contains([]string{"filebrowser", "jellyfin", "cc-connect"}, module.ID) {
			return fmt.Errorf("未知模块 %q", module.ID)
		}
		moduleKey := ModuleKey(module)
		if _, exists := seenModules[moduleKey]; exists {
			return fmt.Errorf("模块实例 %q 重复", moduleKey)
		}
		seenModules[moduleKey] = struct{}{}
		if !module.Enabled {
			continue
		}
		switch module.ID {
		case "filebrowser":
			if module.InstanceID != "" || module.WorkDir != "" {
				return errors.New("FileBrowser 模块不允许设置 instance_id 或 work_dir")
			}
			if !module.ReadOnly {
				return errors.New("FileBrowser 模块必须为只读")
			}
			if err := requireLoopbackURL(module.BaseURL); err != nil {
				return fmt.Errorf("FileBrowser 地址无效: %w", err)
			}
		case "jellyfin":
			if module.InstanceID != "" || module.WorkDir != "" {
				return errors.New("Jellyfin 模块不允许设置 instance_id 或 work_dir")
			}
			if !module.ReadOnly {
				return errors.New("Jellyfin 模块必须为只读")
			}
			if err := requireLoopbackURL(module.BaseURL); err != nil {
				return fmt.Errorf("Jellyfin 地址无效: %w", err)
			}
		case "cc-connect":
			if !moduleInstancePattern.MatchString(module.InstanceID) {
				return errors.New("cc-connect instance_id 格式无效")
			}
			if module.BaseURL != "" || module.ReadOnly {
				return errors.New("cc-connect 不允许设置 base_url 或只读标记")
			}
			if !filepath.IsAbs(module.WorkDir) {
				return errors.New("cc-connect work_dir 必须是绝对路径")
			}
		}
	}
	for _, directory := range config.SharedDirectories {
		if directory.ID == "" || directory.Name == "" {
			return errors.New("共享目录必须包含 ID 和名称")
		}
		for _, permission := range directory.Permissions {
			if permission != "read" && permission != "download" {
				return fmt.Errorf("共享目录包含写权限 %q", permission)
			}
		}
	}
	return nil
}

func ModuleKey(module protocol.ModuleConfigV1) string {
	if module.InstanceID != "" {
		return module.InstanceID
	}
	return module.ID
}

func requireLoopbackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.User != nil || u.Hostname() == "" {
		return errors.New("模块必须使用无凭据的 HTTP 回环地址")
	}
	host := u.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("模块地址必须监听回环网络")
		}
	}
	return nil
}

func requireHTTPSURL(raw, name, requiredPort string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s 地址必须是无凭据、无路径的 HTTPS 地址", name)
	}
	if requiredPort != "" && u.Port() != requiredPort {
		return fmt.Errorf("%s 地址必须明确使用 %s 端口", name, requiredPort)
	}
	return nil
}

func (s *ConfigStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(storedConfig{Signed: s.signed}, "", "  ")
	if err != nil {
		return fmt.Errorf("编码设备配置失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建设备配置目录失败: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".device-config-*")
	if err != nil {
		return fmt.Errorf("创建设备配置临时文件失败: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置设备配置权限失败: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入设备配置失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步设备配置失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭设备配置文件失败: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("替换设备配置失败: %w", err)
	}
	return nil
}
