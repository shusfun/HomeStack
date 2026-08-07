package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsRequiresExplicitTransport(t *testing.T) {
	setRequiredSettings(t)
	t.Setenv("HOMESTACK_CONTROL_TRANSPORT", "")
	if _, err := loadSettings(); err == nil {
		t.Fatal("缺少显式传输模式必须失败")
	}
}

func TestReverseProxyTransportRequiresLoopbackAndNoTLS(t *testing.T) {
	setRequiredSettings(t)
	t.Setenv("HOMESTACK_CONTROL_TRANSPORT", "reverse-proxy")
	t.Setenv("HOMESTACK_CONTROL_ADDR", "127.0.0.1:18443")
	t.Setenv("HOMESTACK_TLS_CERT", "")
	t.Setenv("HOMESTACK_TLS_KEY", "")
	settings, err := loadSettings()
	if err != nil || settings.transport != "reverse-proxy" {
		t.Fatalf("合法反代配置失败: %+v %v", settings, err)
	}
	t.Setenv("HOMESTACK_CONTROL_ADDR", "0.0.0.0:18443")
	if _, err := loadSettings(); err == nil {
		t.Fatal("反代模式绑定公网地址必须失败")
	}
}

func TestLoadEnvFileRejectsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.env")
	if err := os.WriteFile(path, []byte("GOOD=value\nbad line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvFile(path); err == nil {
		t.Fatal("非法环境文件行必须失败")
	}
}

func setRequiredSettings(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"HOMESTACK_CONTROL_TRANSPORT": "reverse-proxy", "HOMESTACK_CONTROL_ADDR": "127.0.0.1:18443",
		"HOMESTACK_PUBLIC_URL": "https://app.example.com", "HOMESTACK_STATE_DIR": "/tmp/state",
		"HOMESTACK_SIGNING_KEY": "/tmp/key", "HOMESTACK_SIGNING_KEY_ID": "control-test",
		"HOMESTACK_GOOGLE_CLIENT_ID": "", "HOMESTACK_GOOGLE_CLIENT_SECRET": "",
		"HOMESTACK_GITHUB_CLIENT_ID": "client", "HOMESTACK_GITHUB_CLIENT_SECRET": "secret",
		"HOMESTACK_TLS_CERT": "", "HOMESTACK_TLS_KEY": "",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}
