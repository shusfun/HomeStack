package setuphelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/maintenance"
	setupapi "github.com/wangshangbin/homestack/internal/setup"
	"go.yaml.in/yaml/v3"
)

func TestMaintenanceStatusMarksInterruptedTaskFailed(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	config := maintenance.Configuration{ControlHost: "app.example.com", PocketHost: "id.example.com", MeshHost: "mesh.example.com", TailHost: "tail.example.com", PublicIPv4: "203.0.113.8"}
	target := config
	target.ControlHost = "new-app.example.com"
	manager := NewMaintenanceManager()
	manager.CompletedPath = filepath.Join(directory, "completed.json")
	manager.StatusPath = filepath.Join(directory, "status.json")
	manager.Now = func() time.Time { return now }
	rolledBack := false
	manager.Rollback = func(context.Context, maintenance.Configuration) error {
		rolledBack = true
		return nil
	}
	completed := persistedState{Phase: setupapi.PhaseCompleted, Config: &config, UpdatedAt: now}
	if err := atomicJSON(manager.CompletedPath, completed, 0o600); err != nil {
		t.Fatal(err)
	}
	active := maintenance.Status{Phase: maintenance.PhaseApplying, Current: &config, Target: &target, UpdatedAt: now.Add(-time.Minute)}
	if err := atomicJSON(manager.StatusPath, active, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil || status.Phase != maintenance.PhaseFailed || !strings.Contains(status.Error, "Helper 重启") || !rolledBack {
		t.Fatalf("中断任务未标记失败: %+v err=%v", status, err)
	}
}

func TestMaintenanceStatusRecoversCommittedTaskAfterRestart(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	current := maintenance.Configuration{ControlHost: "app.example.com", PocketHost: "id.example.com", MeshHost: "mesh.example.com", TailHost: "tail.example.com", PublicIPv4: "203.0.113.8"}
	target := current
	target.ControlHost = "new-app.example.com"
	manager := NewMaintenanceManager()
	manager.CompletedPath = filepath.Join(directory, "completed.json")
	manager.StatusPath = filepath.Join(directory, "status.json")
	manager.Now = func() time.Time { return now }
	rolledBack := false
	manager.Rollback = func(context.Context, maintenance.Configuration) error {
		rolledBack = true
		return nil
	}
	if err := atomicJSON(manager.CompletedPath, persistedState{Phase: setupapi.PhaseCompleted, Config: &target, UpdatedAt: now}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(manager.StatusPath, maintenance.Status{Phase: maintenance.PhaseApplying, Current: &current, Target: &target, UpdatedAt: now.Add(-time.Minute)}, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil || status.Phase != maintenance.PhaseCompleted || status.Current == nil || *status.Current != target || rolledBack {
		t.Fatalf("已提交迁移未从中断状态恢复: %+v rollback=%t err=%v", status, rolledBack, err)
	}
}

func TestMaintenanceRejectsTailDomainChangeWithDevices(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	current := maintenance.Configuration{ControlHost: "app.example.com", PocketHost: "id.example.com", MeshHost: "mesh.example.com", TailHost: "tail.example.com", PublicIPv4: "203.0.113.8"}
	target := current
	target.TailHost = "new-tail.example.com"
	manager := NewMaintenanceManager()
	manager.CompletedPath = filepath.Join(directory, "completed.json")
	manager.StatusPath = filepath.Join(directory, "status.json")
	manager.DevicesPath = filepath.Join(directory, "devices.json")
	manager.Now = func() time.Time { return now }
	if err := atomicJSON(manager.CompletedPath, persistedState{Phase: setupapi.PhaseCompleted, Config: &current, UpdatedAt: now}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.DevicesPath, []byte(`{"device-1":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reconfigure(context.Background(), target); err == nil || !strings.Contains(err.Error(), "已登记设备") {
		t.Fatalf("存在设备时 Tailnet 域名修改未被拒绝: %v", err)
	}
}

func TestUpdateHeadscaleYAMLPreservesUnrelatedConfiguration(t *testing.T) {
	original := []byte("server_url: https://old-mesh.example.com\nnoise:\n  private_key_path: /var/lib/headscale/noise.key\noidc:\n  issuer: https://old-id.example.com\n  client_id: stable-client\ndns:\n  base_domain: old-tail.example.com\nderp:\n  server:\n    ipv4: 203.0.113.8\n")
	target := maintenance.Configuration{MeshHost: "new-mesh.example.com", PocketHost: "new-id.example.com", TailHost: "new-tail.example.com", PublicIPv4: "198.51.100.9"}
	updated, err := updateHeadscaleYAML(original, target)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	noise := document["noise"].(map[string]any)
	oidc := document["oidc"].(map[string]any)
	if noise["private_key_path"] != "/var/lib/headscale/noise.key" || oidc["client_id"] != "stable-client" || oidc["issuer"] != "https://new-id.example.com" {
		encoded, _ := json.Marshal(document)
		t.Fatalf("Headscale 非目标配置未保留或目标未更新: %s", encoded)
	}
}

func TestHelperCommandWhitelistsAreNarrow(t *testing.T) {
	if !allowedCommand("systemctl", []string{"restart", "pocket-id.service"}) || allowedCommand("systemctl", []string{"restart", "ssh.service"}) {
		t.Fatal("Setup Helper systemctl 白名单无效")
	}
	if _, err := runMaintenanceCommand(context.Background(), "systemctl", "restart", "ssh.service"); err == nil {
		t.Fatal("Maintenance Helper 接受了非固定服务")
	}
}

func TestMaintenancePreflightRejectsControlHTMLDefaultPage(t *testing.T) {
	target := maintenance.Configuration{ControlHost: "new-app.example.com", PocketHost: "new-id.example.com", MeshHost: "new-mesh.example.com", TailHost: "tail.example.com", PublicIPv4: "198.51.100.9"}
	manager := NewMaintenanceManager()
	manager.LookupIP = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(target.PublicIPv4)}, nil
	}
	manager.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		contentType := "text/plain"
		body := "ok"
		if request.URL.Hostname() == target.ControlHost {
			contentType = "text/html"
			body = "<html>default</html>"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	if err := manager.preflight(context.Background(), maintenance.Configuration{}, target); err == nil || !strings.Contains(err.Error(), "未返回 HomeStack JSON") {
		t.Fatalf("Control HTML 默认页被误识别为有效反代: %v", err)
	}
}

func TestMaintenanceReconfigurationCommitsTargetAndCleansTemporaryKey(t *testing.T) {
	manager, current, target, callbacks := newTransactionTestManager(t)
	manager.Preflight = func(context.Context, maintenance.Configuration, maintenance.Configuration) error { return nil }
	manager.runReconfigure(current, target)
	status, err := manager.Status(context.Background())
	if err != nil || status.Phase != maintenance.PhaseCompleted {
		t.Fatalf("迁移成功状态错误: %+v err=%v", status, err)
	}
	assertFileContains(t, manager.ControlEnvPath, "HOMESTACK_PUBLIC_URL=https://new-app.example.com")
	assertFileContains(t, manager.PocketEnvPath, "APP_URL=https://new-id.example.com")
	if _, err := os.Stat(manager.PocketKeyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("迁移成功后临时 Pocket Key 未删除: %v", err)
	}
	completed, err := readPersistedState(manager.CompletedPath)
	if err != nil || completed.Config == nil || *completed.Config != target {
		t.Fatalf("迁移成功后完成配置错误: %+v err=%v", completed, err)
	}
	if got := callbacks["homestack-control"]; len(got) != 1 || got[0] != "https://new-app.example.com/auth/callback/pocket" {
		t.Fatalf("Control 最终回调未清理旧域名: %v", got)
	}
	if got := callbacks["homestack-headscale"]; len(got) != 1 || got[0] != "https://new-mesh.example.com/oidc/callback" {
		t.Fatalf("Headscale 最终回调未清理旧域名: %v", got)
	}
}

func TestMaintenanceReconfigurationRollsBackWhenControlRestartFails(t *testing.T) {
	manager, current, target, callbacks := newTransactionTestManager(t)
	failControlRestart := true
	manager.Command = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if name == "systemctl" && strings.Join(arguments, " ") == "restart homestack-control.service" && failControlRestart {
			failControlRestart = false
			return nil, errors.New("control restart failed")
		}
		return nil, nil
	}
	manager.Preflight = func(context.Context, maintenance.Configuration, maintenance.Configuration) error { return nil }
	manager.runReconfigure(current, target)
	status, err := manager.Status(context.Background())
	if err != nil || status.Phase != maintenance.PhaseFailed || !strings.Contains(status.Error, "control restart failed") {
		t.Fatalf("迁移失败状态未保留原始错误: %+v err=%v", status, err)
	}
	assertFileContains(t, manager.ControlEnvPath, "HOMESTACK_PUBLIC_URL=https://app.example.com")
	assertFileContains(t, manager.PocketEnvPath, "APP_URL=https://id.example.com")
	completed, err := readPersistedState(manager.CompletedPath)
	if err != nil || completed.Config == nil || *completed.Config != current {
		t.Fatalf("回滚后完成配置未恢复: %+v err=%v", completed, err)
	}
	if got := callbacks["homestack-control"]; len(got) != 1 || got[0] != "https://app.example.com/auth/callback/pocket" {
		t.Fatalf("回滚后 Control 回调未恢复: %v", got)
	}
	if _, err := os.Stat(manager.PocketKeyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("回滚后临时 Pocket Key 未清理: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func newTransactionTestManager(t *testing.T) (*MaintenanceManager, maintenance.Configuration, maintenance.Configuration, map[string][]string) {
	t.Helper()
	directory := t.TempDir()
	current := maintenance.Configuration{ControlHost: "app.example.com", PocketHost: "id.example.com", MeshHost: "mesh.example.com", TailHost: "tail.example.com", PublicIPv4: "203.0.113.8"}
	target := maintenance.Configuration{ControlHost: "new-app.example.com", PocketHost: "new-id.example.com", MeshHost: "new-mesh.example.com", TailHost: "new-tail.example.com", PublicIPv4: "198.51.100.9"}
	manager := NewMaintenanceManager()
	manager.CompletedPath = filepath.Join(directory, "completed.json")
	manager.StatusPath = filepath.Join(directory, "status.json")
	manager.DevicesPath = filepath.Join(directory, "devices.json")
	manager.ControlEnvPath = filepath.Join(directory, "control.env")
	manager.PocketEnvPath = filepath.Join(directory, "pocket.env")
	manager.PocketKeyPath = filepath.Join(directory, "pocket.key")
	manager.HeadscaleConfigPath = filepath.Join(directory, "headscale.yaml")
	manager.Chown = func(string, ...string) error { return nil }
	manager.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	manager.Random = bytes.NewReader(make([]byte, 64))
	manager.Now = func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }
	callbacks := map[string][]string{}
	manager.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := "{}"
		if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/oidc/clients/") {
			body = `{"name":"HomeStack","callbackURLs":["https://old.example.com/callback"],"logoutCallbackURLs":[],"isPublic":false,"pkceEnabled":true,"isGroupRestricted":true}`
		} else if request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/api/oidc/clients/") {
			var payload struct {
				CallbackURLs []string `json:"callbackURLs"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			callbacks[strings.TrimPrefix(request.URL.Path, "/api/oidc/clients/")] = payload.CallbackURLs
			status = http.StatusNoContent
			body = ""
		} else if request.URL.Path != "/healthz" && request.URL.Path != "/api/v1/health" {
			t.Errorf("未处理维护 HTTP 请求: %s %s", request.Method, request.URL.Path)
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	writeTransactionFile(t, manager.ControlEnvPath, "HOMESTACK_PUBLIC_URL=https://app.example.com\nHOMESTACK_HEADSCALE_URL=https://mesh.example.com\nHOMESTACK_POCKET_ID_ISSUER=https://id.example.com\n")
	writeTransactionFile(t, manager.PocketEnvPath, "APP_URL=https://id.example.com\n")
	writeTransactionFile(t, manager.HeadscaleConfigPath, "server_url: https://mesh.example.com\noidc:\n  issuer: https://id.example.com\ndns:\n  base_domain: tail.example.com\nderp:\n  server:\n    ipv4: 203.0.113.8\nnoise:\n  private_key_path: /var/lib/headscale/noise.key\n")
	if err := atomicJSON(manager.CompletedPath, persistedState{Phase: setupapi.PhaseCompleted, Config: &current, UpdatedAt: manager.Now()}, 0o600); err != nil {
		t.Fatal(err)
	}
	return manager, current, target, callbacks
}

func writeTransactionFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFileContains(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), expected) {
		t.Fatalf("%s 不包含 %q: %s err=%v", path, expected, data, err)
	}
}
