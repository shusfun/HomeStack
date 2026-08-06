package ccconnect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteConfigUsesSafeCodexAndExplicitUsers(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.toml")
	err := WriteConfig(path, []Project{{
		Name: "home-project", WorkDir: workDir, BotID: "bot", BotSecret: "secret", AllowFrom: []string{"zhangsan"}, AdminFrom: []string{"zhangsan"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		`mode = 'suggest'`, `backend = 'app_server'`, `app_server_url = 'stdio'`,
		`mode = 'websocket'`, `allow_from = 'zhangsan'`, `admin_from = 'zhangsan'`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("配置缺少 %q:\n%s", required, text)
		}
	}
	if strings.Contains(text, "management") || strings.Contains(text, `= '*'`) {
		t.Fatalf("配置不应启用 Management API 或通配符:\n%s", text)
	}
}

func TestWriteConfigRejectsWildcard(t *testing.T) {
	err := WriteConfig(filepath.Join(t.TempDir(), "config.toml"), []Project{{
		Name: "home-project", WorkDir: t.TempDir(), BotID: "bot", BotSecret: "secret", AllowFrom: []string{"*"}, AdminFrom: []string{"admin"},
	}})
	if err == nil {
		t.Fatal("allow_from 通配符不应被接受")
	}
}
