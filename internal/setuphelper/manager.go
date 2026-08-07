package setuphelper

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

const staticAPIUserID = "00000000-0000-0000-0000-000000000000"

type asset struct {
	Component   string
	Version     string
	URL         string
	SHA256      string
	Target      string
	VersionArgs []string
}

var linuxAssets = map[string][]asset{
	"amd64": {
		{Component: "headscale", Version: "0.29.3", URL: "https://github.com/juanfont/headscale/releases/download/v0.29.3/headscale_0.29.3_linux_amd64", SHA256: "8dc183758024ed7095cf610fedea0790233613c71353bc8be2715d82ba29b92c", Target: "/usr/local/bin/headscale", VersionArgs: []string{"version"}},
		{Component: "pocket-id", Version: "2.12.0", URL: "https://github.com/pocket-id/pocket-id/releases/download/v2.12.0/pocket-id_linux_amd64", SHA256: "0f27f55f6597986f9998ba0594c9eb4ef73fab01521d2795edf2ebce06c8448e", Target: "/usr/local/bin/pocket-id", VersionArgs: []string{"version"}},
	},
	"arm64": {
		{Component: "headscale", Version: "0.29.3", URL: "https://github.com/juanfont/headscale/releases/download/v0.29.3/headscale_0.29.3_linux_arm64", SHA256: "ecf0099f9aa1efb56e7c74718342a493f7d44a840626a2877ca526e675040f4e", Target: "/usr/local/bin/headscale", VersionArgs: []string{"version"}},
		{Component: "pocket-id", Version: "2.12.0", URL: "https://github.com/pocket-id/pocket-id/releases/download/v2.12.0/pocket-id_linux_arm64", SHA256: "217fa73d99564883b5ec8f4d5fed0a691922308bd757999daddb12ac66f9dd16", Target: "/usr/local/bin/pocket-id", VersionArgs: []string{"version"}},
	},
}

type persistedState struct {
	Phase     setupapi.Phase          `json:"phase"`
	Config    *setupapi.Configuration `json:"config,omitempty"`
	Error     string                  `json:"error,omitempty"`
	UpdatedAt time.Time               `json:"updated_at"`
}

type fileBackup struct {
	Exists bool   `json:"exists"`
	Data   []byte `json:"data,omitempty"`
}

type finalizeBackup struct {
	ControlEnv      fileBackup `json:"control_env"`
	HeadscaleConfig fileBackup `json:"headscale_config"`
	HeadscalePolicy fileBackup `json:"headscale_policy"`
	HeadscaleSecret fileBackup `json:"headscale_secret"`
	SetupToken      fileBackup `json:"setup_token"`
	SetupSession    fileBackup `json:"setup_session"`
}

type Manager struct {
	StatePath          string
	CompletedPath      string
	FinalizeBackupPath string
	TemplateRoot       string
	HTTPClient         *http.Client
	Command            func(context.Context, string, ...string) ([]byte, error)
	Now                func() time.Time
	Random             io.Reader
	mu                 sync.Mutex
}

func NewManager() *Manager {
	return &Manager{
		StatePath: "/var/lib/homestack-setup/state.json", CompletedPath: "/var/lib/homestack-setup/completed.json", FinalizeBackupPath: "/var/lib/homestack-setup/finalize-backup.json",
		TemplateRoot: "/usr/local/share/homestack/deploy", HTTPClient: &http.Client{Timeout: 5 * time.Minute},
		Command: runWhitelistedCommand, Now: time.Now, Random: rand.Reader,
	}
}

func (m *Manager) Status() (setupapi.Status, error) {
	if _, err := os.Stat(m.CompletedPath); err == nil {
		return setupapi.Status{Phase: setupapi.PhaseCompleted, UpdatedAt: m.Now().UTC()}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return setupapi.Status{}, fmt.Errorf("读取 Setup 完成标记失败: %w", err)
	}
	state, err := m.readState()
	if errors.Is(err, os.ErrNotExist) {
		return setupapi.Status{Phase: setupapi.PhaseInfrastructure, UpdatedAt: m.Now().UTC()}, nil
	}
	if err != nil {
		return setupapi.Status{}, err
	}
	return statusFromState(state), nil
}

func (m *Manager) Prepare(ctx context.Context, config setupapi.Configuration) (setupapi.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := setupapi.ValidateConfiguration(config); err != nil {
		return setupapi.Status{}, err
	}
	status, err := m.Status()
	if err != nil {
		return setupapi.Status{}, err
	}
	if status.Phase == setupapi.PhaseCompleted {
		return status, errors.New("Setup 已完成并永久锁定")
	}
	if status.Phase == setupapi.PhasePocketID {
		if status.Config != nil && *status.Config == config {
			return status, nil
		}
		return status, errors.New("基础设施已写入，不允许更换域名后重复 Prepare")
	}
	if runtime.GOOS != "linux" {
		return status, errors.New("Setup Helper 只支持 Linux")
	}
	assets := linuxAssets[runtime.GOARCH]
	if len(assets) != 2 {
		return status, fmt.Errorf("不支持的 Linux 架构: %s", runtime.GOARCH)
	}
	if err := m.writeState(persistedState{Phase: setupapi.PhaseInfrastructure, Config: &config, UpdatedAt: m.Now().UTC()}); err != nil {
		return status, err
	}
	fail := func(failure error) (setupapi.Status, error) {
		if _, statErr := os.Stat("/etc/systemd/system/pocket-id.service"); statErr == nil {
			if _, cleanupErr := m.Command(ctx, "systemctl", "disable", "--now", "pocket-id.service"); cleanupErr != nil {
				failure = fmt.Errorf("%v；停止部分配置的 Pocket ID 失败: %w", failure, cleanupErr)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			failure = fmt.Errorf("%v；检查 Pocket ID unit 失败: %w", failure, statErr)
		}
		return m.recordFailure(config, setupapi.PhaseInfrastructure, failure)
	}
	for _, item := range assets {
		if err := m.installAsset(ctx, item); err != nil {
			return fail(err)
		}
	}
	if err := m.ensureSystemAccounts(ctx); err != nil {
		return fail(err)
	}
	if err := m.preparePocketID(config); err != nil {
		return fail(err)
	}
	if err := m.installUnits("pocket-id.service", "headscale.service", "homestack-control.service", "homestack-setup-switch.service"); err != nil {
		return fail(err)
	}
	if _, err := m.Command(ctx, "systemctl", "daemon-reload"); err != nil {
		return fail(fmt.Errorf("重载 systemd 失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "enable", "pocket-id.service"); err != nil {
		return fail(fmt.Errorf("启用 Pocket ID 失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "pocket-id.service"); err != nil {
		return fail(fmt.Errorf("启动 Pocket ID 失败: %w", err))
	}
	if err := m.waitForPocket(ctx); err != nil {
		return fail(err)
	}
	state := persistedState{Phase: setupapi.PhasePocketID, Config: &config, UpdatedAt: m.Now().UTC()}
	if err := m.writeState(state); err != nil {
		return fail(err)
	}
	return statusFromState(state), nil
}

func (m *Manager) Finalize(ctx context.Context) (setupapi.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readState()
	if err != nil {
		return setupapi.Status{}, fmt.Errorf("读取 Setup 状态失败: %w", err)
	}
	if state.Phase == setupapi.PhaseCompleted {
		return statusFromState(state), errors.New("Setup 已完成并永久锁定")
	}
	if state.Phase == setupapi.PhaseFinalize {
		return statusFromState(state), nil
	}
	if state.Phase != setupapi.PhasePocketID || state.Config == nil {
		return statusFromState(state), errors.New("必须先完成基础设施 Prepare")
	}
	if err := m.ensureFinalizeBackup(); err != nil {
		return m.recordFailure(*state.Config, setupapi.PhasePocketID, err)
	}
	fail := func(failure error) (setupapi.Status, error) {
		var rollbackErrors []string
		if _, err := m.Command(ctx, "systemctl", "stop", "homestack-control.service", "headscale.service"); err != nil {
			rollbackErrors = append(rollbackErrors, "停止正式服务失败: "+err.Error())
		}
		if err := m.restoreFinalizeBackup(); err != nil {
			rollbackErrors = append(rollbackErrors, err.Error())
		}
		if len(rollbackErrors) > 0 {
			failure = fmt.Errorf("%v；回滚失败: %s", failure, strings.Join(rollbackErrors, "；"))
		}
		return m.recordFailure(*state.Config, setupapi.PhasePocketID, failure)
	}
	apiKey, err := os.ReadFile("/etc/pocket-id/static-api-key")
	if err != nil {
		return fail(fmt.Errorf("读取 Pocket ID 临时 API Key 失败: %w", err))
	}
	client := pocketClient{baseURL: "http://127.0.0.1:8444", apiKey: strings.TrimSpace(string(apiKey)), client: m.HTTPClient}
	admin, err := client.initialAdmin(ctx)
	if err != nil {
		return fail(err)
	}
	groupID, err := client.createGroup(ctx, admin)
	if err != nil {
		return fail(err)
	}
	controlSecret, err := randomSecret(m.Random)
	if err != nil {
		return fail(err)
	}
	headscaleSecret, err := randomSecret(m.Random)
	if err != nil {
		return fail(err)
	}
	if err := client.createOIDCClients(ctx, *state.Config, groupID, controlSecret, headscaleSecret); err != nil {
		return fail(err)
	}
	if err := m.writeFinalConfiguration(*state.Config, controlSecret, headscaleSecret); err != nil {
		return fail(err)
	}
	if _, err := m.Command(ctx, "/usr/local/bin/headscale", "configtest", "--config", "/etc/headscale/config.yaml"); err != nil {
		return fail(fmt.Errorf("Headscale configtest 失败: %w", err))
	}
	if _, err := m.Command(ctx, "/usr/local/bin/headscale", "policy", "check", "--config", "/etc/headscale/config.yaml", "--bypass"); err != nil {
		return fail(fmt.Errorf("Headscale policy 检查失败: %w", err))
	}
	if _, err := m.Command(ctx, "/usr/local/bin/homestack-control", "configtest", "--env-file", "/etc/homestack/control.env"); err != nil {
		return fail(fmt.Errorf("Control configtest 失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "enable", "headscale.service", "homestack-control.service"); err != nil {
		return fail(fmt.Errorf("启用 HomeStack 服务失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "headscale.service"); err != nil {
		return fail(fmt.Errorf("启动 Headscale 失败: %w", err))
	}
	finalizing := persistedState{Phase: setupapi.PhaseFinalize, Config: state.Config, UpdatedAt: m.Now().UTC()}
	if err := m.writeState(finalizing); err != nil {
		return fail(err)
	}
	if _, err := m.Command(ctx, "systemctl", "start", "--no-block", "homestack-setup-switch.service"); err != nil {
		return fail(fmt.Errorf("启动正式服务切换任务失败: %w", err))
	}
	return statusFromState(finalizing), nil
}

func (m *Manager) CompleteSwitch(ctx context.Context) error {
	state, err := m.readState()
	if err != nil {
		return fmt.Errorf("读取 Setup 切换状态失败: %w", err)
	}
	if state.Phase != setupapi.PhaseFinalize || state.Config == nil {
		return errors.New("Setup 未处于 finalize 阶段")
	}
	rollback := func(failure error, envBackup, keyBackup []byte) error {
		var rollbackErrors []string
		if len(envBackup) > 0 {
			if err := atomicWrite("/etc/pocket-id/pocket-id.env", envBackup, 0o600); err != nil {
				rollbackErrors = append(rollbackErrors, "恢复 Pocket ID 环境失败: "+err.Error())
			} else if err := chownFiles("pocket-id", "/etc/pocket-id/pocket-id.env"); err != nil {
				rollbackErrors = append(rollbackErrors, "恢复 Pocket ID 环境权限失败: "+err.Error())
			}
		}
		if len(keyBackup) > 0 {
			if err := atomicWrite("/etc/pocket-id/static-api-key", keyBackup, 0o600); err != nil {
				rollbackErrors = append(rollbackErrors, "恢复 Pocket ID API Key 失败: "+err.Error())
			} else if err := chownFiles("pocket-id", "/etc/pocket-id/static-api-key"); err != nil {
				rollbackErrors = append(rollbackErrors, "恢复 API Key 权限失败: "+err.Error())
			}
		}
		if err := m.restoreFinalizeBackup(); err != nil {
			rollbackErrors = append(rollbackErrors, err.Error())
		}
		for _, command := range [][]string{{"disable", "--now", "homestack-maintenance-helper.service"}, {"stop", "homestack-control.service", "headscale.service"}, {"restart", "pocket-id.service"}, {"enable", "homestack-setup-helper.service", "homestack-setup.service"}, {"restart", "homestack-setup-helper.service"}, {"restart", "homestack-setup.service"}} {
			if _, err := m.Command(ctx, "systemctl", command...); err != nil {
				rollbackErrors = append(rollbackErrors, strings.Join(command, " ")+": "+err.Error())
			}
		}
		message := failure.Error()
		if len(rollbackErrors) > 0 {
			message += "；回滚失败: " + strings.Join(rollbackErrors, "；")
		}
		_, _ = m.recordFailure(*state.Config, setupapi.PhasePocketID, errors.New(message))
		return errors.New(message)
	}
	if _, err := m.Command(ctx, "systemctl", "stop", "homestack-setup.service"); err != nil {
		return rollback(fmt.Errorf("停止 Setup 服务失败: %w", err), nil, nil)
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "homestack-control.service"); err != nil {
		return rollback(fmt.Errorf("启动 Control 失败: %w", err), nil, nil)
	}
	if err := m.waitForURL(ctx, "http://127.0.0.1:8443/api/v1/health", 30*time.Second); err != nil {
		return rollback(fmt.Errorf("Control 健康检查失败: %w", err), nil, nil)
	}
	envBackup, err := os.ReadFile("/etc/pocket-id/pocket-id.env")
	if err != nil {
		return rollback(fmt.Errorf("备份 Pocket ID 环境失败: %w", err), nil, nil)
	}
	keyBackup, err := os.ReadFile("/etc/pocket-id/static-api-key")
	if err != nil {
		return rollback(fmt.Errorf("备份 Pocket ID API Key 失败: %w", err), nil, nil)
	}
	rollbackCompleted := func(failure error) error {
		if removeErr := os.Remove(m.CompletedPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			failure = fmt.Errorf("%v；撤销 Setup 完成标记失败: %w", failure, removeErr)
		}
		return rollback(failure, envBackup, keyBackup)
	}
	if err := removeStaticAPIKey("/etc/pocket-id/pocket-id.env", "/etc/pocket-id/static-api-key"); err != nil {
		return rollback(err, envBackup, keyBackup)
	}
	if err := chownFiles("pocket-id", "/etc/pocket-id/pocket-id.env"); err != nil {
		return rollback(fmt.Errorf("设置 Pocket ID 环境权限失败: %w", err), envBackup, keyBackup)
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "pocket-id.service"); err != nil {
		return rollback(fmt.Errorf("关闭 Pocket ID 临时 API Key 失败: %w", err), envBackup, keyBackup)
	}
	if err := m.waitForPocket(ctx); err != nil {
		return rollback(err, envBackup, keyBackup)
	}
	for _, path := range []string{"/etc/homestack/setup-token.sha256", "/etc/homestack/setup-session.json"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return rollback(fmt.Errorf("删除 Setup 凭据 %s 失败: %w", path, err), envBackup, keyBackup)
		}
	}
	completed := persistedState{Phase: setupapi.PhaseCompleted, Config: state.Config, UpdatedAt: m.Now().UTC()}
	if err := atomicJSON(m.CompletedPath, completed, 0o600); err != nil {
		return rollback(fmt.Errorf("写入 Setup 完成标记失败: %w", err), envBackup, keyBackup)
	}
	if err := m.writeState(completed); err != nil {
		return rollbackCompleted(fmt.Errorf("写入 Setup 完成状态失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "enable", "homestack-maintenance-helper.service"); err != nil {
		return rollbackCompleted(fmt.Errorf("启用维护 Helper 失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "homestack-maintenance-helper.service"); err != nil {
		return rollbackCompleted(fmt.Errorf("启动维护 Helper 失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "disable", "homestack-setup.service", "homestack-setup-helper.service"); err != nil {
		return rollbackCompleted(fmt.Errorf("锁定 Setup 服务失败: %w", err))
	}
	if err := os.Remove(m.FinalizeBackupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return rollbackCompleted(fmt.Errorf("删除 Finalize 回滚快照失败: %w", err))
	}
	return nil
}

func (m *Manager) ensureFinalizeBackup() error {
	if _, err := os.Stat(m.FinalizeBackupPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 Finalize 回滚快照失败: %w", err)
	}
	backup := finalizeBackup{}
	items := []struct {
		path   string
		target *fileBackup
	}{
		{"/etc/homestack/control.env", &backup.ControlEnv},
		{"/etc/headscale/config.yaml", &backup.HeadscaleConfig},
		{"/etc/headscale/policy.hujson", &backup.HeadscalePolicy},
		{"/etc/headscale/oidc-client-secret", &backup.HeadscaleSecret},
		{"/etc/homestack/setup-token.sha256", &backup.SetupToken},
		{"/etc/homestack/setup-session.json", &backup.SetupSession},
	}
	for _, item := range items {
		data, err := os.ReadFile(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("备份 %s 失败: %w", item.path, err)
		}
		item.target.Exists = true
		item.target.Data = data
	}
	if err := atomicJSON(m.FinalizeBackupPath, backup, 0o600); err != nil {
		return fmt.Errorf("保存 Finalize 回滚快照失败: %w", err)
	}
	return nil
}

func (m *Manager) restoreFinalizeBackup() error {
	data, err := os.ReadFile(m.FinalizeBackupPath)
	if err != nil {
		return fmt.Errorf("读取 Finalize 回滚快照失败: %w", err)
	}
	var backup finalizeBackup
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&backup); err != nil {
		return fmt.Errorf("解析 Finalize 回滚快照失败: %w", err)
	}
	items := []struct {
		path    string
		backup  fileBackup
		mode    os.FileMode
		account string
	}{
		{"/etc/homestack/control.env", backup.ControlEnv, 0o600, "homestack-control"},
		{"/etc/headscale/config.yaml", backup.HeadscaleConfig, 0o640, "headscale"},
		{"/etc/headscale/policy.hujson", backup.HeadscalePolicy, 0o640, "headscale"},
		{"/etc/headscale/oidc-client-secret", backup.HeadscaleSecret, 0o640, "headscale"},
		{"/etc/homestack/setup-token.sha256", backup.SetupToken, 0o400, "homestack-control"},
		{"/etc/homestack/setup-session.json", backup.SetupSession, 0o600, "homestack-control"},
	}
	var failures []string
	for _, item := range items {
		if !item.backup.Exists {
			if err := os.Remove(item.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Sprintf("删除新增配置 %s 失败: %v", item.path, err))
			}
			continue
		}
		if err := atomicWrite(item.path, item.backup.Data, item.mode); err != nil {
			failures = append(failures, fmt.Sprintf("恢复 %s 失败: %v", item.path, err))
			continue
		}
		if err := chownFiles(item.account, item.path); err != nil {
			failures = append(failures, fmt.Sprintf("恢复 %s 权限失败: %v", item.path, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "；"))
	}
	return nil
}

func (m *Manager) installAsset(ctx context.Context, item asset) error {
	if data, err := m.Command(ctx, item.Target, item.VersionArgs...); err == nil && strings.Contains(string(data), item.Version) {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return err
	}
	response, err := m.HTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("下载 %s %s 失败: %w", item.Component, item.Version, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s %s 失败: HTTP %d", item.Component, item.Version, response.StatusCode)
	}
	directory := filepath.Dir(item.Target)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".homestack-component-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, 256<<20)); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入 %s 下载文件失败: %w", item.Component, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != item.SHA256 {
		return fmt.Errorf("%s SHA-256 不匹配: %s", item.Component, actual)
	}
	if err := os.Chmod(temporaryPath, 0o755); err != nil {
		return err
	}
	output, err := m.Command(ctx, temporaryPath, item.VersionArgs...)
	if err != nil || !strings.Contains(string(output), item.Version) {
		return fmt.Errorf("%s 版本校验失败: %w: %s", item.Component, err, strings.TrimSpace(string(output)))
	}
	if err := os.Rename(temporaryPath, item.Target); err != nil {
		return fmt.Errorf("安装 %s 失败: %w", item.Component, err)
	}
	return nil
}

func (m *Manager) ensureSystemAccounts(ctx context.Context) error {
	_ = ctx
	for _, account := range []string{"pocket-id", "headscale", "homestack-control"} {
		if _, err := user.Lookup(account); err != nil {
			return fmt.Errorf("安装器未创建系统用户 %s: %w", account, err)
		}
	}
	return nil
}

func (m *Manager) preparePocketID(config setupapi.Configuration) error {
	for _, directory := range []string{"/etc/pocket-id", "/var/lib/pocket-id", "/var/lib/pocket-id/uploads", "/etc/headscale", "/var/lib/headscale", "/etc/homestack", "/var/lib/homestack-setup"} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", directory, err)
		}
	}
	if err := chownFiles("pocket-id", "/etc/pocket-id", "/var/lib/pocket-id", "/var/lib/pocket-id/uploads"); err != nil {
		return err
	}
	if err := chownFiles("headscale", "/etc/headscale", "/var/lib/headscale"); err != nil {
		return err
	}
	if err := ensureRandomFile("/etc/pocket-id/encryption-key", 32, 0o600, m.Random); err != nil {
		return err
	}
	if err := ensureRandomFile("/etc/pocket-id/static-api-key", 32, 0o600, m.Random); err != nil {
		return err
	}
	if err := chownFiles("pocket-id", "/etc/pocket-id/encryption-key", "/etc/pocket-id/static-api-key"); err != nil {
		return err
	}
	env := strings.Join([]string{
		"APP_ENV=production", "APP_URL=https://" + config.PocketHost, "INTERNAL_APP_URL=http://127.0.0.1:8444",
		"HOST=127.0.0.1", "PORT=8444", "ACTORS_HOST=127.0.0.1", "ACTORS_PORT=1414",
		"ENCRYPTION_KEY_FILE=/etc/pocket-id/encryption-key", "STATIC_API_KEY_FILE=/etc/pocket-id/static-api-key",
		"TRUST_PROXY=127.0.0.1/32,::1/128", "ALLOW_INSECURE_CALLBACK_URLS=false", "VERSION_CHECK_DISABLED=true", "ANALYTICS_DISABLED=true",
		"DB_CONNECTION_STRING=/var/lib/pocket-id/pocket-id.db", "UPLOAD_PATH=/var/lib/pocket-id/uploads", "LOG_LEVEL=info", "LOG_JSON=true", "",
	}, "\n")
	if err := atomicWrite("/etc/pocket-id/pocket-id.env", []byte(env), 0o600); err != nil {
		return err
	}
	return chownFiles("pocket-id", "/etc/pocket-id/pocket-id.env")
}

func (m *Manager) writeFinalConfiguration(config setupapi.Configuration, controlSecret, headscaleSecret string) error {
	headscaleTemplate, err := os.ReadFile(filepath.Join(m.TemplateRoot, "headscale/config.yaml"))
	if err != nil {
		return fmt.Errorf("读取 Headscale 模板失败: %w", err)
	}
	headscale := strings.NewReplacer(
		"mesh.example.com", config.MeshHost, "id.example.com", config.PocketHost, "tail.example.com", config.TailHost,
		"REPLACE_WITH_VPS_IPV4", config.PublicIPv4, "REPLACE_WITH_HEADSCALE_OIDC_CLIENT_ID", "homestack-headscale",
	).Replace(string(headscaleTemplate))
	if strings.Contains(headscale, "REPLACE_WITH_") || strings.Contains(headscale, "example.com") {
		return errors.New("Headscale 模板仍包含占位符")
	}
	if err := atomicWrite("/etc/headscale/config.yaml", []byte(headscale), 0o640); err != nil {
		return err
	}
	policy, err := os.ReadFile(filepath.Join(m.TemplateRoot, "headscale/policy.hujson"))
	if err != nil {
		return err
	}
	if err := atomicWrite("/etc/headscale/policy.hujson", policy, 0o640); err != nil {
		return err
	}
	if err := atomicWrite("/etc/headscale/oidc-client-secret", []byte(headscaleSecret+"\n"), 0o640); err != nil {
		return err
	}
	if err := chownFiles("headscale", "/etc/headscale/config.yaml", "/etc/headscale/policy.hujson", "/etc/headscale/oidc-client-secret"); err != nil {
		return err
	}
	controlEnv := strings.Join([]string{
		"HOMESTACK_CONTROL_TRANSPORT=reverse-proxy", "HOMESTACK_CONTROL_ADDR=127.0.0.1:8443", "HOMESTACK_PUBLIC_URL=https://" + config.ControlHost,
		"HOMESTACK_HEADSCALE_URL=https://" + config.MeshHost, "HOMESTACK_POCKET_ID_ISSUER=https://" + config.PocketHost,
		"HOMESTACK_POCKET_ID_CLIENT_ID=homestack-control", "HOMESTACK_POCKET_ID_CLIENT_SECRET=" + controlSecret,
		"HOMESTACK_GOOGLE_CLIENT_ID=", "HOMESTACK_GOOGLE_CLIENT_SECRET=", "HOMESTACK_GITHUB_CLIENT_ID=", "HOMESTACK_GITHUB_CLIENT_SECRET=",
		"HOMESTACK_STATE_DIR=/var/lib/homestack-control", "HOMESTACK_HEADSCALE_CONFIG=/etc/headscale/config.yaml",
		"HOMESTACK_TLS_CERT=", "HOMESTACK_TLS_KEY=", "HOMESTACK_SIGNING_KEY=/etc/homestack/signing-private.key", "HOMESTACK_SIGNING_KEY_ID=homestack-control-2026-01", "",
	}, "\n")
	if err := atomicWrite("/etc/homestack/control.env", []byte(controlEnv), 0o600); err != nil {
		return err
	}
	return chownFiles("homestack-control", "/etc/homestack/control.env")
}

func (m *Manager) installUnits(names ...string) error {
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(m.TemplateRoot, "systemd", name))
		if err != nil {
			return fmt.Errorf("读取 systemd 模板 %s 失败: %w", name, err)
		}
		if err := atomicWrite(filepath.Join("/etc/systemd/system", name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) waitForPocket(ctx context.Context) error {
	return m.waitForURL(ctx, "http://127.0.0.1:8444/healthz", 30*time.Second)
}

func (m *Manager) waitForURL(ctx context.Context, target string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		response, err := m.HTTPClient.Do(request)
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

func (m *Manager) readState() (persistedState, error) {
	data, err := os.ReadFile(m.StatePath)
	if err != nil {
		return persistedState{}, err
	}
	var state persistedState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return persistedState{}, fmt.Errorf("解析 Setup 状态失败: %w", err)
	}
	return state, nil
}

func (m *Manager) writeState(state persistedState) error {
	return atomicJSON(m.StatePath, state, 0o600)
}

func (m *Manager) recordFailure(config setupapi.Configuration, phase setupapi.Phase, failure error) (setupapi.Status, error) {
	state := persistedState{Phase: phase, Config: &config, Error: failure.Error(), UpdatedAt: m.Now().UTC()}
	if err := m.writeState(state); err != nil {
		return statusFromState(state), fmt.Errorf("%v；并且写入失败状态失败: %w", failure, err)
	}
	return statusFromState(state), failure
}

func statusFromState(state persistedState) setupapi.Status {
	status := setupapi.Status{Phase: state.Phase, Config: state.Config, Error: state.Error, UpdatedAt: state.UpdatedAt}
	if state.Config != nil {
		status.PocketURL = "https://" + state.Config.PocketHost + "/setup"
	}
	return status
}

func atomicJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), mode)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".homestack-write-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func ensureRandomFile(path string, size int, mode os.FileMode, reader io.Reader) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	return atomicWrite(path, []byte(base64.RawURLEncoding.EncodeToString(data)+"\n"), mode)
}

func randomSecret(reader io.Reader) (string, error) {
	data := make([]byte, 32)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func chownFiles(account string, paths ...string) error {
	entry, err := user.Lookup(account)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(entry.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(entry.Gid)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

func removeStaticAPIKey(envPath, keyPath string) error {
	data, err := os.ReadFile(envPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "STATIC_API_KEY") {
			filtered = append(filtered, line)
		}
	}
	if err := atomicWrite(envPath, []byte(strings.Join(filtered, "\n")), 0o600); err != nil {
		return err
	}
	if err := os.Remove(keyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除 Pocket ID 临时 API Key 失败: %w", err)
	}
	return nil
}

func runWhitelistedCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	if !allowedCommand(name, arguments) {
		return nil, fmt.Errorf("命令不在 Setup Helper 白名单中: %s %s", name, strings.Join(arguments, " "))
	}
	output, err := exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func allowedCommand(name string, arguments []string) bool {
	joined := strings.Join(arguments, "\x00")
	if name == "systemctl" {
		allowed := map[string]bool{
			"daemon-reload":                                                        true,
			"enable\x00pocket-id.service":                                          true,
			"restart\x00pocket-id.service":                                         true,
			"disable\x00--now\x00pocket-id.service":                                true,
			"enable\x00headscale.service\x00homestack-control.service":             true,
			"restart\x00headscale.service":                                         true,
			"start\x00--no-block\x00homestack-setup-switch.service":                true,
			"stop\x00homestack-setup.service":                                      true,
			"restart\x00homestack-control.service":                                 true,
			"stop\x00homestack-control.service\x00headscale.service":               true,
			"enable\x00homestack-setup-helper.service\x00homestack-setup.service":  true,
			"restart\x00homestack-setup-helper.service":                            true,
			"restart\x00homestack-setup.service":                                   true,
			"enable\x00homestack-maintenance-helper.service":                       true,
			"restart\x00homestack-maintenance-helper.service":                      true,
			"disable\x00--now\x00homestack-maintenance-helper.service":             true,
			"disable\x00homestack-setup.service\x00homestack-setup-helper.service": true,
		}
		return allowed[joined]
	}
	if name == "/usr/local/bin/headscale" {
		return joined == "configtest\x00--config\x00/etc/headscale/config.yaml" || joined == "policy\x00check\x00--config\x00/etc/headscale/config.yaml\x00--bypass" || joined == "version"
	}
	if name == "/usr/local/bin/pocket-id" {
		return joined == "version"
	}
	if name == "/usr/local/bin/homestack-control" {
		return joined == "configtest\x00--env-file\x00/etc/homestack/control.env"
	}
	return strings.HasPrefix(filepath.Base(name), ".homestack-component-") && joined == "version"
}
