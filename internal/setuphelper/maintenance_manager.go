package setuphelper

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/wangshangbin/homestack/internal/maintenance"
	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

type MaintenanceManager struct {
	CompletedPath       string
	StatusPath          string
	DevicesPath         string
	ControlEnvPath      string
	PocketEnvPath       string
	PocketKeyPath       string
	HeadscaleConfigPath string
	HTTPClient          *http.Client
	Command             func(context.Context, string, ...string) ([]byte, error)
	Chown               func(string, ...string) error
	LookupIP            func(context.Context, string, string) ([]net.IP, error)
	Now                 func() time.Time
	Random              io.Reader
	Preflight           func(context.Context, maintenance.Configuration, maintenance.Configuration) error
	Rollback            func(context.Context, maintenance.Configuration) error
	mu                  sync.Mutex
	statusMu            sync.Mutex
	running             atomic.Bool
}

func NewMaintenanceManager() *MaintenanceManager {
	m := &MaintenanceManager{
		CompletedPath:       "/var/lib/homestack-setup/completed.json",
		StatusPath:          "/var/lib/homestack-maintenance/reconfigure.json",
		DevicesPath:         "/var/lib/homestack-control/devices.json",
		ControlEnvPath:      "/etc/homestack/control.env",
		PocketEnvPath:       "/etc/pocket-id/pocket-id.env",
		PocketKeyPath:       "/etc/pocket-id/static-api-key",
		HeadscaleConfigPath: "/etc/headscale/config.yaml",
		HTTPClient:          &http.Client{Timeout: 15 * time.Second},
		Command:             runMaintenanceCommand,
		Chown:               chownFiles,
		LookupIP:            net.DefaultResolver.LookupIP,
		Now:                 time.Now,
		Random:              rand.Reader,
	}
	m.Preflight = m.preflight
	m.Rollback = m.rollbackReconfiguration
	return m
}

func (m *MaintenanceManager) Configuration(context.Context) (maintenance.Configuration, error) {
	state, err := readPersistedState(m.CompletedPath)
	if err != nil {
		return maintenance.Configuration{}, fmt.Errorf("读取已完成配置失败: %w", err)
	}
	if state.Phase != setupapi.PhaseCompleted || state.Config == nil {
		return maintenance.Configuration{}, errors.New("Setup 尚未完成")
	}
	return *state.Config, nil
}

func (m *MaintenanceManager) Status(ctx context.Context) (maintenance.Status, error) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	data, err := os.ReadFile(m.StatusPath)
	if errors.Is(err, os.ErrNotExist) {
		current, currentErr := m.Configuration(ctx)
		if currentErr != nil {
			return maintenance.Status{}, currentErr
		}
		return maintenance.Status{Phase: maintenance.PhaseIdle, Current: &current, UpdatedAt: m.Now().UTC()}, nil
	}
	if err != nil {
		return maintenance.Status{}, fmt.Errorf("读取域名迁移状态失败: %w", err)
	}
	var status maintenance.Status
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		return maintenance.Status{}, fmt.Errorf("解析域名迁移状态失败: %w", err)
	}
	if !m.running.Load() && (status.Phase == maintenance.PhasePreflight || status.Phase == maintenance.PhaseApplying || status.Phase == maintenance.PhaseRollback) {
		interruptedPhase := status.Phase
		if interruptedPhase == maintenance.PhaseApplying && status.Target != nil {
			completed, completedErr := readPersistedState(m.CompletedPath)
			if completedErr == nil && completed.Phase == setupapi.PhaseCompleted && completed.Config != nil && *completed.Config == *status.Target {
				if err := os.Remove(m.StatusPath + ".backup"); err != nil && !errors.Is(err, os.ErrNotExist) {
					return maintenance.Status{}, fmt.Errorf("清理已提交迁移的回滚数据失败: %w", err)
				}
				status.Phase = maintenance.PhaseCompleted
				status.Current = status.Target
				status.Error = ""
				status.UpdatedAt = m.Now().UTC()
				if err := m.writeStatus(status); err != nil {
					return maintenance.Status{}, fmt.Errorf("恢复已提交迁移状态失败: %w", err)
				}
				return status, nil
			}
		}
		var rollbackErr error
		if interruptedPhase == maintenance.PhaseApplying || interruptedPhase == maintenance.PhaseRollback {
			status.Phase = maintenance.PhaseRollback
			status.UpdatedAt = m.Now().UTC()
			if err := m.writeStatus(status); err != nil {
				return maintenance.Status{}, fmt.Errorf("记录中断任务回滚状态失败: %w", err)
			}
			if status.Current == nil {
				rollbackErr = errors.New("中断的域名迁移缺少原始配置")
			} else {
				rollbackErr = m.Rollback(ctx, *status.Current)
			}
		}
		status.Phase = maintenance.PhaseFailed
		status.Error = "维护 Helper 重启导致域名迁移任务中断"
		if rollbackErr != nil {
			status.Error += "；回滚失败: " + rollbackErr.Error()
		}
		status.UpdatedAt = m.Now().UTC()
		if err := m.writeStatus(status); err != nil {
			return maintenance.Status{}, fmt.Errorf("记录中断的域名迁移任务失败: %w", err)
		}
	}
	return status, nil
}

func (m *MaintenanceManager) Reconfigure(ctx context.Context, target maintenance.Configuration) (maintenance.Status, error) {
	if err := setupapi.ValidateConfiguration(target); err != nil {
		return maintenance.Status{}, err
	}
	current, err := m.Configuration(ctx)
	if err != nil {
		return maintenance.Status{}, err
	}
	if current == target {
		return maintenance.Status{}, errors.New("新配置与当前配置相同")
	}
	if current.TailHost != target.TailHost {
		count, err := deviceCount(m.DevicesPath)
		if err != nil {
			return maintenance.Status{}, err
		}
		if count > 0 {
			return maintenance.Status{}, errors.New("存在已登记设备时禁止修改 Tailnet 基础域名")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status, err := m.Status(ctx)
	if err != nil {
		return maintenance.Status{}, err
	}
	if status.Phase == maintenance.PhasePreflight || status.Phase == maintenance.PhaseApplying || status.Phase == maintenance.PhaseRollback {
		return status, errors.New("已有域名迁移任务正在执行")
	}
	status = maintenance.Status{Phase: maintenance.PhasePreflight, Current: &current, Target: &target, TargetURL: "https://" + target.ControlHost, UpdatedAt: m.Now().UTC()}
	if err := m.writeStatus(status); err != nil {
		return maintenance.Status{}, err
	}
	m.running.Store(true)
	go m.runReconfigure(current, target)
	return status, nil
}

func (m *MaintenanceManager) runReconfigure(current, target maintenance.Configuration) {
	defer m.running.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := m.Preflight(ctx, current, target); err != nil {
		m.finish(current, target, maintenance.PhaseFailed, err)
		return
	}
	status := maintenance.Status{Phase: maintenance.PhaseApplying, Current: &current, Target: &target, TargetURL: "https://" + target.ControlHost, UpdatedAt: m.Now().UTC()}
	if err := m.writeStatus(status); err != nil {
		return
	}
	if err := m.applyReconfiguration(ctx, current, target); err != nil {
		m.finish(current, target, maintenance.PhaseRollback, err)
		rollbackErr := m.Rollback(ctx, current)
		if rollbackErr != nil {
			err = fmt.Errorf("%v；回滚失败: %w", err, rollbackErr)
		}
		m.finish(current, target, maintenance.PhaseFailed, err)
		return
	}
	if err := os.Remove(m.StatusPath + ".backup"); err != nil && !errors.Is(err, os.ErrNotExist) {
		failure := fmt.Errorf("删除迁移回滚数据失败: %w", err)
		m.finish(current, target, maintenance.PhaseRollback, failure)
		if rollbackErr := m.Rollback(ctx, current); rollbackErr != nil {
			failure = fmt.Errorf("%v；回滚失败: %w", failure, rollbackErr)
		}
		m.finish(current, target, maintenance.PhaseFailed, failure)
		return
	}
	m.finish(target, target, maintenance.PhaseCompleted, nil)
}

type configurationBackup struct {
	control, pocket, headscale []byte
	apiKey                     []byte
}

func (m *MaintenanceManager) applyReconfiguration(ctx context.Context, current, target maintenance.Configuration) error {
	backup, err := m.readConfigurationBackup()
	if err != nil {
		return err
	}
	key, err := randomSecret(m.Random)
	if err != nil {
		return err
	}
	backupValues := map[string]string{
		"control": string(backup.control), "pocket": string(backup.pocket), "headscale": string(backup.headscale),
		"original_api_key": string(backup.apiKey), "temporary_api_key": key,
	}
	if err := atomicJSON(m.StatusPath+".backup", backupValues, 0o600); err != nil {
		return fmt.Errorf("保存迁移回滚数据失败: %w", err)
	}
	if err := enableTemporaryPocketKey(m.PocketEnvPath, m.PocketKeyPath, backup.pocket, key, m.Chown); err != nil {
		return err
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "pocket-id.service"); err != nil {
		return fmt.Errorf("启用 Pocket ID 临时 API Key 失败: %w", err)
	}
	if err := waitForHTTP(ctx, m.HTTPClient, "http://127.0.0.1:8444/healthz", 30*time.Second); err != nil {
		return err
	}
	client := pocketClient{baseURL: "http://127.0.0.1:8444", apiKey: key, client: m.HTTPClient}
	if err := client.updateClientCallbacks(ctx, "homestack-control", uniqueStrings("https://"+current.ControlHost+"/auth/callback/pocket", "https://"+target.ControlHost+"/auth/callback/pocket")); err != nil {
		return err
	}
	if err := client.updateClientCallbacks(ctx, "homestack-headscale", uniqueStrings("https://"+current.MeshHost+"/oidc/callback", "https://"+target.MeshHost+"/oidc/callback")); err != nil {
		return err
	}
	control, err := replaceEnvValues(backup.control, map[string]string{
		"HOMESTACK_PUBLIC_URL":       "https://" + target.ControlHost,
		"HOMESTACK_HEADSCALE_URL":    "https://" + target.MeshHost,
		"HOMESTACK_POCKET_ID_ISSUER": "https://" + target.PocketHost,
	})
	if err != nil {
		return err
	}
	pocket, err := replaceEnvValues(backup.pocket, map[string]string{"APP_URL": "https://" + target.PocketHost, "STATIC_API_KEY_FILE": m.PocketKeyPath})
	if err != nil {
		return err
	}
	headscale, err := updateHeadscaleYAML(backup.headscale, target)
	if err != nil {
		return err
	}
	if err := atomicWrite(m.ControlEnvPath, control, 0o600); err != nil {
		return err
	}
	if err := m.Chown("homestack-control", m.ControlEnvPath); err != nil {
		return err
	}
	if err := atomicWrite(m.PocketEnvPath, pocket, 0o600); err != nil {
		return err
	}
	if err := m.Chown("pocket-id", m.PocketEnvPath); err != nil {
		return err
	}
	if err := atomicWrite(m.HeadscaleConfigPath, headscale, 0o640); err != nil {
		return err
	}
	if err := m.Chown("headscale", m.HeadscaleConfigPath); err != nil {
		return err
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "pocket-id.service"); err != nil {
		return fmt.Errorf("切换 Pocket ID 公网地址失败: %w", err)
	}
	if err := waitForHTTP(ctx, m.HTTPClient, "http://127.0.0.1:8444/healthz", 30*time.Second); err != nil {
		return err
	}
	for _, command := range [][]string{{"/usr/local/bin/headscale", "configtest", "--config", "/etc/headscale/config.yaml"}, {"/usr/local/bin/headscale", "policy", "check", "--config", "/etc/headscale/config.yaml", "--bypass"}, {"/usr/local/bin/homestack-control", "configtest", "--env-file", "/etc/homestack/control.env"}} {
		if _, err := m.Command(ctx, command[0], command[1:]...); err != nil {
			return err
		}
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "headscale.service"); err != nil {
		return fmt.Errorf("重启 Headscale 失败: %w", err)
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "homestack-control.service"); err != nil {
		return fmt.Errorf("重启 Control 失败: %w", err)
	}
	if err := waitForHTTP(ctx, m.HTTPClient, "http://127.0.0.1:8443/api/v1/health", 30*time.Second); err != nil {
		return err
	}
	if err := client.updateClientCallbacks(ctx, "homestack-control", []string{"https://" + target.ControlHost + "/auth/callback/pocket"}); err != nil {
		return err
	}
	if err := client.updateClientCallbacks(ctx, "homestack-headscale", []string{"https://" + target.MeshHost + "/oidc/callback"}); err != nil {
		return err
	}
	if err := removeStaticAPIKey(m.PocketEnvPath, m.PocketKeyPath); err != nil {
		return err
	}
	if err := m.Chown("pocket-id", m.PocketEnvPath); err != nil {
		return err
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "pocket-id.service"); err != nil {
		return err
	}
	if err := waitForHTTP(ctx, m.HTTPClient, "http://127.0.0.1:8444/healthz", 30*time.Second); err != nil {
		return err
	}
	completed := persistedState{Phase: setupapi.PhaseCompleted, Config: &target, UpdatedAt: m.Now().UTC()}
	if err := atomicJSON(m.CompletedPath, completed, 0o600); err != nil {
		return err
	}
	return nil
}

func (m *MaintenanceManager) rollbackReconfiguration(ctx context.Context, current maintenance.Configuration) error {
	data, err := os.ReadFile(m.StatusPath + ".backup")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取迁移回滚数据失败: %w", err)
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	var failures []string
	for _, item := range []struct{ path, key string }{{m.ControlEnvPath, "control"}, {m.PocketEnvPath, "pocket"}, {m.HeadscaleConfigPath, "headscale"}} {
		mode := os.FileMode(0o600)
		if item.path == m.HeadscaleConfigPath {
			mode = 0o640
		}
		if err := atomicWrite(item.path, []byte(values[item.key]), mode); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if err := m.Chown("homestack-control", m.ControlEnvPath); err != nil {
		failures = append(failures, err.Error())
	}
	if err := m.Chown("pocket-id", m.PocketEnvPath); err != nil {
		failures = append(failures, err.Error())
	}
	temporaryKey := values["temporary_api_key"]
	pocketWithKey, envErr := replaceEnvValues([]byte(values["pocket"]), map[string]string{"STATIC_API_KEY_FILE": m.PocketKeyPath})
	if envErr != nil {
		failures = append(failures, envErr.Error())
	} else if err := atomicWrite(m.PocketEnvPath, pocketWithKey, 0o600); err != nil {
		failures = append(failures, err.Error())
	} else if err := m.Chown("pocket-id", m.PocketEnvPath); err != nil {
		failures = append(failures, err.Error())
	}
	if err := atomicWrite(m.PocketKeyPath, []byte(temporaryKey+"\n"), 0o600); err != nil {
		failures = append(failures, err.Error())
	}
	if err := m.Chown("pocket-id", m.PocketKeyPath); err != nil {
		failures = append(failures, err.Error())
	}
	if err := m.Chown("headscale", m.HeadscaleConfigPath); err != nil {
		failures = append(failures, err.Error())
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "pocket-id.service"); err != nil {
		failures = append(failures, err.Error())
	}
	if err := waitForHTTP(ctx, m.HTTPClient, "http://127.0.0.1:8444/healthz", 30*time.Second); err != nil {
		failures = append(failures, err.Error())
	} else {
		client := pocketClient{baseURL: "http://127.0.0.1:8444", apiKey: temporaryKey, client: m.HTTPClient}
		if err := client.updateClientCallbacks(ctx, "homestack-control", []string{"https://" + current.ControlHost + "/auth/callback/pocket"}); err != nil {
			failures = append(failures, err.Error())
		}
		if err := client.updateClientCallbacks(ctx, "homestack-headscale", []string{"https://" + current.MeshHost + "/oidc/callback"}); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if err := atomicWrite(m.PocketEnvPath, []byte(values["pocket"]), 0o600); err != nil {
		failures = append(failures, err.Error())
	}
	if err := m.Chown("pocket-id", m.PocketEnvPath); err != nil {
		failures = append(failures, err.Error())
	}
	if values["original_api_key"] == "" {
		if err := os.Remove(m.PocketKeyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err.Error())
		}
	} else if err := atomicWrite(m.PocketKeyPath, []byte(values["original_api_key"]), 0o600); err != nil {
		failures = append(failures, err.Error())
	} else if err := m.Chown("pocket-id", m.PocketKeyPath); err != nil {
		failures = append(failures, err.Error())
	}
	for _, unit := range []string{"pocket-id.service", "headscale.service", "homestack-control.service"} {
		if _, err := m.Command(ctx, "systemctl", "restart", unit); err != nil {
			failures = append(failures, err.Error())
		}
	}
	completed := persistedState{Phase: setupapi.PhaseCompleted, Config: &current, UpdatedAt: m.Now().UTC()}
	if err := atomicJSON(m.CompletedPath, completed, 0o600); err != nil {
		failures = append(failures, "恢复已完成配置失败: "+err.Error())
	}
	if err := os.Remove(m.StatusPath + ".backup"); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, "删除迁移回滚数据失败: "+err.Error())
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "；"))
	}
	return nil
}

func (m *MaintenanceManager) preflight(ctx context.Context, _ maintenance.Configuration, target maintenance.Configuration) error {
	expected := net.ParseIP(target.PublicIPv4).To4()
	for _, host := range []string{target.ControlHost, target.PocketHost, target.MeshHost} {
		addresses, err := m.LookupIP(ctx, "ip4", host)
		if err != nil {
			return fmt.Errorf("解析域名 %s 失败: %w", host, err)
		}
		matched := false
		for _, address := range addresses {
			if address.To4() != nil && address.To4().Equal(expected) {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("域名 %s 未直接解析到 VPS 公网 IPv4 %s", host, target.PublicIPv4)
		}
	}
	checks := []struct {
		endpoint   string
		controlAPI bool
	}{{"https://" + target.ControlHost + "/api/v1/meta", true}, {"https://" + target.PocketHost + "/healthz", false}, {"https://" + target.MeshHost + "/health", false}}
	for _, check := range checks {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, check.endpoint, nil)
		response, err := m.HTTPClient.Do(request)
		if err != nil {
			return fmt.Errorf("新域名 HTTPS/反向代理检查失败 %s: %w", check.endpoint, err)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			return fmt.Errorf("新域名 HTTPS/反向代理检查失败 %s: HTTP %d", check.endpoint, response.StatusCode)
		}
		if check.controlAPI {
			var meta struct {
				Version string `json:"version"`
			}
			contentType := response.Header.Get("Content-Type")
			if !strings.Contains(contentType, "application/json") || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&meta) != nil || meta.Version == "" {
				_ = response.Body.Close()
				return fmt.Errorf("新 Control 域名未返回 HomeStack JSON 元数据: %s", check.endpoint)
			}
		}
		_ = response.Body.Close()
	}
	return nil
}

func (m *MaintenanceManager) finish(current, target maintenance.Configuration, phase maintenance.Phase, failure error) {
	status := maintenance.Status{Phase: phase, Current: &current, Target: &target, TargetURL: "https://" + target.ControlHost, UpdatedAt: m.Now().UTC()}
	if failure != nil {
		status.Error = failure.Error()
	}
	_ = m.writeStatus(status)
}

func (m *MaintenanceManager) writeStatus(status maintenance.Status) error {
	return atomicJSON(m.StatusPath, status, 0o600)
}

func readPersistedState(path string) (persistedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return persistedState{}, err
	}
	var state persistedState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return persistedState{}, err
	}
	return state, nil
}

func (m *MaintenanceManager) readConfigurationBackup() (configurationBackup, error) {
	var result configurationBackup
	files := []struct {
		path   string
		target *[]byte
	}{{m.ControlEnvPath, &result.control}, {m.PocketEnvPath, &result.pocket}, {m.HeadscaleConfigPath, &result.headscale}}
	for _, item := range files {
		data, err := os.ReadFile(item.path)
		if err != nil {
			return result, fmt.Errorf("备份 %s 失败: %w", item.path, err)
		}
		*item.target = data
	}
	if data, err := os.ReadFile(m.PocketKeyPath); err == nil {
		result.apiKey = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	return result, nil
}

func enableTemporaryPocketKey(envPath, keyPath string, env []byte, key string, chown func(string, ...string) error) error {
	if err := atomicWrite(keyPath, []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	if err := chown("pocket-id", keyPath); err != nil {
		return err
	}
	updated, err := replaceEnvValues(env, map[string]string{"STATIC_API_KEY_FILE": keyPath})
	if err != nil {
		return err
	}
	if err := atomicWrite(envPath, updated, 0o600); err != nil {
		return err
	}
	return chown("pocket-id", envPath)
}

func replaceEnvValues(data []byte, replacements map[string]string) ([]byte, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	seen := map[string]bool{}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, _, found := strings.Cut(trimmed, "=")
		if !found || name == "" {
			return nil, fmt.Errorf("环境文件第 %d 行格式无效", index+1)
		}
		if value, ok := replacements[name]; ok {
			lines[index] = name + "=" + value
			seen[name] = true
		}
	}
	for name, value := range replacements {
		if !seen[name] {
			lines = append(lines, name+"="+value)
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func updateHeadscaleYAML(data []byte, config maintenance.Configuration) ([]byte, error) {
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("解析 Headscale YAML 失败: %w", err)
	}
	setYAMLValue(document, []string{"server_url"}, "https://"+config.MeshHost)
	setYAMLValue(document, []string{"oidc", "issuer"}, "https://"+config.PocketHost)
	setYAMLValue(document, []string{"dns", "base_domain"}, config.TailHost)
	setYAMLValue(document, []string{"derp", "server", "ipv4"}, config.PublicIPv4)
	result, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("编码 Headscale YAML 失败: %w", err)
	}
	return result, nil
}

func setYAMLValue(root map[string]any, path []string, value any) {
	cursor := root
	for _, key := range path[:len(path)-1] {
		next, ok := cursor[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cursor[key] = next
		}
		cursor = next
	}
	cursor[path[len(path)-1]] = value
}

func deviceCount(path string) (int, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取设备状态失败: %w", err)
	}
	var devices map[string]json.RawMessage
	if err := json.Unmarshal(data, &devices); err != nil {
		return 0, fmt.Errorf("解析设备状态失败: %w", err)
	}
	return len(devices), nil
}

func waitForHTTP(ctx context.Context, client *http.Client, target string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s 未在 %s 内通过健康检查", target, timeout)
}

func uniqueStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func runMaintenanceCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	joined := strings.Join(arguments, "\x00")
	allowed := false
	if name == "systemctl" {
		allowed = joined == "restart\x00pocket-id.service" || joined == "restart\x00headscale.service" || joined == "restart\x00homestack-control.service"
	} else if name == "/usr/local/bin/headscale" {
		allowed = joined == "configtest\x00--config\x00/etc/headscale/config.yaml" || joined == "policy\x00check\x00--config\x00/etc/headscale/config.yaml\x00--bypass"
	} else if name == "/usr/local/bin/homestack-control" {
		allowed = joined == "configtest\x00--env-file\x00/etc/homestack/control.env"
	}
	if !allowed {
		return nil, fmt.Errorf("命令不在维护 Helper 白名单中: %s %s", name, strings.Join(arguments, " "))
	}
	return runCommandUnchecked(ctx, name, arguments...)
}

func runCommandUnchecked(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
