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
	config := setupapi.Configuration{PublicHost: "home.example.com", Providers: map[string]setupapi.ProviderCredentials{"github": {ClientID: "client", ClientSecret: "top-secret"}}}
	status, err := manager.Prepare(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != setupapi.PhaseIdentity || status.Config == nil || len(status.Config.Providers) != 1 || status.Config.Providers[0].ClientID != "client" {
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

func TestCompletedManagerPreservesSecretAndSupportsProviderLink(t *testing.T) {
	directory := t.TempDir()
	manager := testManager(directory)
	current := setupapi.Configuration{PublicHost: "old.example.com", Providers: map[string]setupapi.ProviderCredentials{"google": {ClientID: "client", ClientSecret: "secret"}}}
	if err := atomicWrite(manager.ControlEnv, controlEnvironment(current), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(manager.CompletedPath, persistedState{Phase: setupapi.PhaseCompleted}, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := manager.ReconfigureDomain(context.Background(), "https://new.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if status.Config == nil || status.Config.PublicHost != "new.example.com" {
		t.Fatalf("迁移状态错误: %+v", status)
	}
	if !strings.Contains(string(mustRead(t, manager.ControlEnv)), "HOMESTACK_GOOGLE_CLIENT_SECRET=secret") {
		t.Fatal("域名迁移未保留 OAuth Secret")
	}
	if _, err := manager.LinkProvider(context.Background(), "github", setupapi.ProviderCredentials{ClientID: "other", ClientSecret: "other-secret"}); err != nil {
		t.Fatalf("绑定第二登录方式失败: %v", err)
	}
	if !strings.Contains(string(mustRead(t, manager.ControlEnv)), "HOMESTACK_GITHUB_CLIENT_SECRET=other-secret") {
		t.Fatal("第二登录方式未写入 Control 配置")
	}
}

func TestManagerMigratesSingleProviderEnvironmentOnce(t *testing.T) {
	directory := t.TempDir()
	manager := testManager(directory)
	manager.MigrationBackupPath = filepath.Join(directory, "control.env.pre-0.2.1")
	legacy := "HOMESTACK_CONTROL_TRANSPORT=reverse-proxy\nHOMESTACK_CONTROL_ADDR=127.0.0.1:18443\nHOMESTACK_PUBLIC_URL=https://home.example.com\nHOMESTACK_STATE_DIR=/var/lib/homestack-control\nHOMESTACK_SIGNING_KEY=/etc/homestack/control-signing.key\nHOMESTACK_SIGNING_KEY_ID=homestack-control\nHOMESTACK_OAUTH_PROVIDER=github\nHOMESTACK_OAUTH_CLIENT_ID=client\nHOMESTACK_OAUTH_CLIENT_SECRET=secret\n"
	if err := os.WriteFile(manager.ControlEnv, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	migrated, err := manager.MigrateControlEnvironment()
	if err != nil || !migrated {
		t.Fatalf("迁移失败: migrated=%v err=%v", migrated, err)
	}
	current := string(mustRead(t, manager.ControlEnv))
	if strings.Contains(current, "HOMESTACK_OAUTH_") || !strings.Contains(current, "HOMESTACK_GITHUB_CLIENT_SECRET=secret") {
		t.Fatalf("迁移结果错误: %s", current)
	}
	if string(mustRead(t, manager.MigrationBackupPath)) != legacy {
		t.Fatal("旧配置备份不完整")
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
