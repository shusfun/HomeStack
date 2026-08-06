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
		mutate func(*protocol.SignedDeviceConfigV1)
	}{
		{name: "Control 使用 HTTP", mutate: func(config *protocol.SignedDeviceConfigV1) { config.ControlURL = "http://app.example.com:8443" }},
		{name: "Agent 端口错误", mutate: func(config *protocol.SignedDeviceConfigV1) { config.AgentURL = "https://device.tailnet.example:443" }},
		{name: "有效期过长", mutate: func(config *protocol.SignedDeviceConfigV1) { config.ExpiresAt = now.Add(72 * time.Hour) }},
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

func validConfig(now time.Time, revision uint64) protocol.SignedDeviceConfigV1 {
	return protocol.SignedDeviceConfigV1{
		Version:    protocol.DeviceConfigVersion,
		DeviceID:   "device-1",
		DeviceName: "设备一",
		Revision:   revision,
		IssuedAt:   now,
		ExpiresAt:  now.Add(time.Hour),
		ControlURL: "https://app.example.com:8443",
		AgentURL:   "https://device.tailnet.example:9443",
		Modules: []protocol.ModuleConfigV1{{
			ID: "filebrowser", Enabled: true, BaseURL: "http://127.0.0.1:8080", ReadOnly: true,
		}},
		SharedDirectories: []protocol.SharedDirectoryV1{{ID: "media", Name: "媒体", Permissions: []string{"read", "download"}}},
	}
}
