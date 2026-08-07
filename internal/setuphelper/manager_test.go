package setuphelper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

func TestManagerWritesOnlyControlConfigurationAndRedactsSecret(t *testing.T) {
	directory := t.TempDir()
	manager := testManager(directory)
	config := setupapi.Configuration{PublicHost: "home.example.com", Provider: "github", ClientID: "client", ClientSecret: "top-secret"}
	status, err := manager.Prepare(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != setupapi.PhaseIdentity || status.Config == nil || status.Config.ClientID != "client" {
		t.Fatalf("Prepare 状态错误: %+v", status)
	}
	encoded := string(mustRead(t, manager.StatePath))
	if !strings.Contains(encoded, "top-secret") {
		t.Fatal("Helper 状态未保留 Finalize 所需 Secret")
	}
	public, _ := manager.Status()
	marshaled := public.Config
	if marshaled == nil || marshaled.PublicHost != "home.example.com" {
		t.Fatalf("公开配置错误: %+v", marshaled)
	}
	info, err := os.Stat(manager.ControlEnv)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Control 配置权限为 %o", info.Mode().Perm())
	}
}

func TestCompletedManagerPreservesSecretAndSupportsExplicitProviderSwitch(t *testing.T) {
	directory := t.TempDir()
	manager := testManager(directory)
	current := setupapi.Configuration{PublicHost: "old.example.com", Provider: "google", ClientID: "client", ClientSecret: "secret"}
	if err := atomicWrite(manager.ControlEnv, controlEnvironment(current), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(manager.CompletedPath, persistedState{Phase: setupapi.PhaseCompleted}, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Reconfigure(context.Background(), setupapi.Configuration{PublicHost: "new.example.com", Provider: "google", ClientID: "client"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Config == nil || status.Config.PublicHost != "new.example.com" {
		t.Fatalf("迁移状态错误: %+v", status)
	}
	if !strings.Contains(string(mustRead(t, manager.ControlEnv)), "HOMESTACK_OAUTH_CLIENT_SECRET=secret") {
		t.Fatal("域名迁移未保留 OAuth Secret")
	}
	if _, err := manager.Reconfigure(context.Background(), setupapi.Configuration{PublicHost: "new.example.com", Provider: "github", ClientID: "other", ClientSecret: "other-secret"}); err != nil {
		t.Fatalf("显式登录源切换失败: %v", err)
	}
}

func testManager(directory string) *Manager {
	return &Manager{StatePath: filepath.Join(directory, "state.json"), CompletedPath: filepath.Join(directory, "completed.json"), BackupPath: filepath.Join(directory, "backup.json"), ControlEnv: filepath.Join(directory, "control.env"), TokenPath: filepath.Join(directory, "token"), SessionPath: filepath.Join(directory, "session"), Now: func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }, Chown: func(string, ...string) error { return nil }, Command: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
