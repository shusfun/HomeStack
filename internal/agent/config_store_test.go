package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/secure"
)

func TestConfigStoreRejectsRollbackAndExpiry(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store, err := OpenConfigStore("", "device-1", publicKey, "control-1")
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	config := validConfig(now, 2)
	signed, err := secure.SignJWS(privateKey, "control-1", config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(signed); err != nil {
		t.Fatal(err)
	}
	config.Revision = 1
	signed, err = secure.SignJWS(privateKey, "control-1", config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(signed); !errors.Is(err, ErrConfigRollback) {
		t.Fatalf("期望配置回退错误，实际为 %v", err)
	}
	config.Revision = 3
	config.ExpiresAt = now
	signed, err = secure.SignJWS(privateKey, "control-1", config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(signed); err == nil {
		t.Fatal("过期配置不应被接受")
	}
}

func TestValidateDeviceConfigRejectsInsecureEndpoints(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*protocol.SignedDeviceConfig)
	}{
		{name: "Control 使用 HTTP", mutate: func(config *protocol.SignedDeviceConfig) { config.ControlURL = "http://app.example.com" }},
		{name: "Node 端口错误", mutate: func(config *protocol.SignedDeviceConfig) { config.AgentURL = "https://device.tail-name.ts.net:443" }},
		{name: "有效期过长", mutate: func(config *protocol.SignedDeviceConfig) { config.ExpiresAt = now.Add(72 * time.Hour) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig(now, 1)
			test.mutate(&config)
			if err := ValidateDeviceConfig(config); err == nil {
				t.Fatal("不安全的设备配置不应通过校验")
			}
		})
	}
}

func TestValidateDeviceConfigForPlatformAcceptsCanonicalWindowsPaths(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	config := validConfig(now, 1)
	config.SharedDirectories = []protocol.SharedDirectory{
		{ID: "downloads", Name: "下载", Path: `C:\Users\test\Downloads`},
		{ID: "media", Name: "媒体", Path: `\\nas\media`},
	}
	config.Modules = append(config.Modules, protocol.ModuleConfig{
		ID: "cc-connect", InstanceID: "cc-main", Enabled: true, WorkDir: `C:\Users\test\.cc-connect`,
	})
	if err := ValidateDeviceConfigForPlatform(config, "windows"); err != nil {
		t.Fatalf("规范 Windows 路径被拒绝: %v", err)
	}
}

func TestValidateDeviceConfigForPlatformRejectsNonCanonicalWindowsPaths(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	for name, directoryPath := range map[string]string{
		"盘符相对路径":  `C:Users\test\Downloads`,
		"正斜杠":     `C:/Users/test/Downloads`,
		"父目录段":    `C:\Users\test\..\Downloads`,
		"重复分隔符":   `C:\Users\\test\Downloads`,
		"不完整 UNC": `\\nas`,
	} {
		t.Run(name, func(t *testing.T) {
			config := validConfig(now, 1)
			config.SharedDirectories = []protocol.SharedDirectory{{ID: "downloads", Name: "下载", Path: directoryPath}}
			if err := ValidateDeviceConfigForPlatform(config, "windows"); err == nil {
				t.Fatal("非规范 Windows 共享目录未被拒绝")
			}
		})
	}
}

func TestValidateDeviceConfigForPlatformRejectsNonCanonicalWindowsWorkDir(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	config := validConfig(now, 1)
	config.Modules = append(config.Modules, protocol.ModuleConfig{
		ID: "cc-connect", InstanceID: "cc-main", Enabled: true, WorkDir: `C:/Users/test/.cc-connect`,
	})
	if err := ValidateDeviceConfigForPlatform(config, "windows"); err == nil {
		t.Fatal("非规范 Windows work_dir 未被拒绝")
	}
}

func validConfig(now time.Time, revision uint64) protocol.SignedDeviceConfig {
	return protocol.SignedDeviceConfig{
		DeviceID:   "device-1",
		DeviceName: "设备一",
		Revision:   revision,
		IssuedAt:   now,
		ExpiresAt:  now.Add(time.Hour),
		ControlURL: "https://app.example.com",
		AgentURL:   "https://device.tail-name.ts.net:19443",
		Modules: []protocol.ModuleConfig{{
			ID: "jellyfin", Enabled: true, BaseURL: "http://127.0.0.1:8096", ReadOnly: true,
		}},
		SharedDirectories: []protocol.SharedDirectory{{ID: "media", Name: "媒体", Path: "/srv/media"}},
	}
}
