package setuphelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

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

// Manager 是唯一拥有 Control 系统配置写权限的 Helper 核心。
type Manager struct {
	StatePath     string
	CompletedPath string
	BackupPath    string
	ControlEnv    string
	TokenPath     string
	SessionPath   string
	HTTPClient    *http.Client
	Command       func(context.Context, string, ...string) ([]byte, error)
	Chown         func(string, ...string) error
	Now           func() time.Time
	mu            sync.Mutex
}

func NewManager() *Manager {
	return &Manager{
		StatePath: "/var/lib/homestack-setup/state.json", CompletedPath: "/var/lib/homestack-setup/completed.json",
		BackupPath: "/var/lib/homestack-setup/control-env.backup.json", ControlEnv: "/etc/homestack/control.env",
		TokenPath: "/etc/homestack/setup-token.sha256", SessionPath: "/etc/homestack/setup-session.json",
		HTTPClient: &http.Client{Timeout: 5 * time.Second}, Command: runWhitelistedCommand, Chown: chownFiles, Now: time.Now,
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
		return setupapi.Status{Phase: setupapi.PhaseDomain, UpdatedAt: m.Now().UTC()}, nil
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
	if status.Phase == setupapi.PhaseCompleted || status.Phase == setupapi.PhaseFinalize {
		return status, errors.New("Setup 已锁定，不能修改配置")
	}
	if status.Phase == setupapi.PhaseIdentity && samePublicConfig(status.Config, config) {
		return status, nil
	}
	if err := m.ensureBackup(); err != nil {
		return m.recordFailure(config, setupapi.PhaseDomain, err)
	}
	if err := m.writeState(persistedState{Phase: setupapi.PhaseDomain, Config: &config, UpdatedAt: m.Now().UTC()}); err != nil {
		return setupapi.Status{}, err
	}
	env := controlEnvironment(config)
	if err := atomicWrite(m.ControlEnv, env, 0o600); err != nil {
		return m.rollbackPrepare(config, err)
	}
	if err := m.Chown("homestack-control", m.ControlEnv); err != nil {
		return m.rollbackPrepare(config, fmt.Errorf("设置 Control 配置权限失败: %w", err))
	}
	if _, err := m.Command(ctx, "/usr/local/bin/homestack-control", "configtest", "--env-file", m.ControlEnv); err != nil {
		return m.rollbackPrepare(config, fmt.Errorf("Control configtest 失败: %w", err))
	}
	state := persistedState{Phase: setupapi.PhaseIdentity, Config: &config, UpdatedAt: m.Now().UTC()}
	if err := m.writeState(state); err != nil {
		return m.rollbackPrepare(config, err)
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
	if state.Phase != setupapi.PhaseIdentity || state.Config == nil {
		return statusFromState(state), errors.New("必须先保存域名与登录配置")
	}
	finalizing := persistedState{Phase: setupapi.PhaseFinalize, Config: state.Config, UpdatedAt: m.Now().UTC()}
	if err := m.writeState(finalizing); err != nil {
		return statusFromState(state), err
	}
	if _, err := m.Command(ctx, "systemctl", "start", "--no-block", "homestack-setup-switch.service"); err != nil {
		_, _ = m.recordFailure(*state.Config, setupapi.PhaseIdentity, err)
		return statusFromState(state), fmt.Errorf("启动正式服务切换任务失败: %w", err)
	}
	return statusFromState(finalizing), nil
}

func (m *Manager) Configuration() (setupapi.PublicConfiguration, error) {
	if _, err := os.Stat(m.CompletedPath); err != nil {
		return setupapi.PublicConfiguration{}, errors.New("Setup 尚未完成")
	}
	config, err := m.readControlEnvironment()
	if err != nil {
		return setupapi.PublicConfiguration{}, err
	}
	return setupapi.PublicConfiguration{PublicHost: config.PublicHost, Provider: config.Provider, ClientID: config.ClientID}, nil
}

func (m *Manager) Reconfigure(ctx context.Context, config setupapi.Configuration) (setupapi.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Stat(m.CompletedPath); err != nil {
		return setupapi.Status{}, errors.New("Setup 尚未完成")
	}
	current, err := m.readControlEnvironment()
	if err != nil {
		return setupapi.Status{}, err
	}
	if config.Provider == current.Provider && config.ClientID == current.ClientID && config.ClientSecret == "" {
		config.ClientSecret = current.ClientSecret
	}
	if err := setupapi.ValidateConfiguration(config); err != nil {
		return setupapi.Status{}, err
	}
	backup, err := os.ReadFile(m.ControlEnv)
	if err != nil {
		return setupapi.Status{}, err
	}
	rollback := func(failure error) (setupapi.Status, error) {
		if restoreErr := atomicWrite(m.ControlEnv, backup, 0o600); restoreErr != nil {
			failure = fmt.Errorf("%v；恢复 Control 配置失败: %w", failure, restoreErr)
		}
		return setupapi.Status{}, failure
	}
	if err := atomicWrite(m.ControlEnv, controlEnvironment(config), 0o600); err != nil {
		return setupapi.Status{}, err
	}
	if err := m.Chown("homestack-control", m.ControlEnv); err != nil {
		return rollback(err)
	}
	if _, err := m.Command(ctx, "/usr/local/bin/homestack-control", "configtest", "--env-file", m.ControlEnv); err != nil {
		return rollback(err)
	}
	if _, err := m.Command(ctx, "systemd-run", "--unit=homestack-control-restart", "--replace", "--on-active=2s", "--property=Type=oneshot", "systemctl", "restart", "homestack-control.service"); err != nil {
		return rollback(err)
	}
	public := setupapi.PublicConfiguration{PublicHost: config.PublicHost, Provider: config.Provider, ClientID: config.ClientID}
	return setupapi.Status{Phase: setupapi.PhaseCompleted, Config: &public, UpdatedAt: m.Now().UTC()}, nil
}

func (m *Manager) CompleteSwitch(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readState()
	if err != nil || state.Phase != setupapi.PhaseFinalize || state.Config == nil {
		return errors.New("Setup 未处于 finalize 阶段")
	}
	rollback := func(failure error) error {
		var failures []string
		if err := m.restoreBackup(); err != nil {
			failures = append(failures, err.Error())
		}
		for _, args := range [][]string{{"stop", "homestack-control.service"}, {"enable", "homestack-config-helper.service", "homestack-setup.service"}, {"restart", "homestack-config-helper.service"}, {"restart", "homestack-setup.service"}} {
			if _, commandErr := m.Command(ctx, "systemctl", args...); commandErr != nil {
				failures = append(failures, commandErr.Error())
			}
		}
		if len(failures) > 0 {
			failure = fmt.Errorf("%v；回滚失败: %s", failure, strings.Join(failures, "；"))
		}
		_, _ = m.recordFailure(*state.Config, setupapi.PhaseIdentity, failure)
		return failure
	}
	if _, err := m.Command(ctx, "systemctl", "stop", "homestack-setup.service"); err != nil {
		return rollback(fmt.Errorf("停止 Setup 服务失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "enable", "homestack-control.service"); err != nil {
		return rollback(fmt.Errorf("启用 Control 失败: %w", err))
	}
	if _, err := m.Command(ctx, "systemctl", "restart", "homestack-control.service"); err != nil {
		return rollback(fmt.Errorf("启动 Control 失败: %w", err))
	}
	if err := m.waitForHealth(ctx); err != nil {
		return rollback(err)
	}
	completed := persistedState{Phase: setupapi.PhaseCompleted, UpdatedAt: m.Now().UTC()}
	if err := atomicJSON(m.CompletedPath, completed, 0o600); err != nil {
		return rollback(fmt.Errorf("写入 Setup 完成标记失败: %w", err))
	}
	if err := m.writeState(completed); err != nil {
		_ = os.Remove(m.CompletedPath)
		return rollback(fmt.Errorf("写入 Setup 完成状态失败: %w", err))
	}
	for _, path := range []string{m.TokenPath, m.SessionPath, m.BackupPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除 Setup 临时文件 %s 失败: %w", path, err)
		}
	}
	if _, err := m.Command(ctx, "systemctl", "disable", "homestack-setup.service"); err != nil {
		return fmt.Errorf("永久锁定 Setup 失败: %w", err)
	}
	return nil
}

func (m *Manager) readControlEnvironment() (setupapi.Configuration, error) {
	data, err := os.ReadFile(m.ControlEnv)
	if err != nil {
		return setupapi.Configuration{}, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			return setupapi.Configuration{}, errors.New("Control 环境文件格式无效")
		}
		values[name] = value
	}
	publicURL := strings.TrimPrefix(values["HOMESTACK_PUBLIC_URL"], "https://")
	config := setupapi.Configuration{PublicHost: publicURL, Provider: values["HOMESTACK_OAUTH_PROVIDER"], ClientID: values["HOMESTACK_OAUTH_CLIENT_ID"], ClientSecret: values["HOMESTACK_OAUTH_CLIENT_SECRET"]}
	if err := setupapi.ValidateConfiguration(config); err != nil {
		return setupapi.Configuration{}, fmt.Errorf("读取当前 Control 配置失败: %w", err)
	}
	return config, nil
}

func controlEnvironment(config setupapi.Configuration) []byte {
	prefix := "HOMESTACK_CONTROL_TRANSPORT=reverse-proxy\n" +
		"HOMESTACK_CONTROL_ADDR=127.0.0.1:18443\n" +
		"HOMESTACK_PUBLIC_URL=https://" + config.PublicHost + "\n" +
		"HOMESTACK_STATE_DIR=/var/lib/homestack-control\n" +
		"HOMESTACK_SIGNING_KEY=/etc/homestack/control-signing.key\n" +
		"HOMESTACK_SIGNING_KEY_ID=homestack-control\n"
	return []byte(prefix + "HOMESTACK_OAUTH_PROVIDER=" + config.Provider + "\n" +
		"HOMESTACK_OAUTH_CLIENT_ID=" + config.ClientID + "\n" +
		"HOMESTACK_OAUTH_CLIENT_SECRET=" + config.ClientSecret + "\n")
}

func samePublicConfig(current *setupapi.PublicConfiguration, target setupapi.Configuration) bool {
	return current != nil && current.PublicHost == target.PublicHost && current.Provider == target.Provider && current.ClientID == target.ClientID
}

func statusFromState(state persistedState) setupapi.Status {
	status := setupapi.Status{Phase: state.Phase, Error: state.Error, UpdatedAt: state.UpdatedAt}
	if state.Config != nil {
		status.Config = &setupapi.PublicConfiguration{PublicHost: state.Config.PublicHost, Provider: state.Config.Provider, ClientID: state.Config.ClientID}
	}
	return status
}

func (m *Manager) recordFailure(config setupapi.Configuration, phase setupapi.Phase, failure error) (setupapi.Status, error) {
	state := persistedState{Phase: phase, Config: &config, Error: failure.Error(), UpdatedAt: m.Now().UTC()}
	if err := m.writeState(state); err != nil {
		return statusFromState(state), fmt.Errorf("%v；记录 Setup 失败状态失败: %w", failure, err)
	}
	return statusFromState(state), failure
}

func (m *Manager) rollbackPrepare(config setupapi.Configuration, failure error) (setupapi.Status, error) {
	if err := m.restoreBackup(); err != nil {
		failure = fmt.Errorf("%v；恢复原 Control 配置失败: %w", failure, err)
	}
	return m.recordFailure(config, setupapi.PhaseDomain, failure)
}

func (m *Manager) ensureBackup() error {
	if _, err := os.Stat(m.BackupPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	backup := fileBackup{}
	data, err := os.ReadFile(m.ControlEnv)
	if err == nil {
		backup.Exists, backup.Data = true, data
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("备份 Control 配置失败: %w", err)
	}
	return atomicJSON(m.BackupPath, backup, 0o600)
}

func (m *Manager) restoreBackup() error {
	data, err := os.ReadFile(m.BackupPath)
	if err != nil {
		return fmt.Errorf("读取 Control 配置备份失败: %w", err)
	}
	var backup fileBackup
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&backup); err != nil {
		return fmt.Errorf("解析 Control 配置备份失败: %w", err)
	}
	if !backup.Exists {
		if err := os.Remove(m.ControlEnv); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := atomicWrite(m.ControlEnv, backup.Data, 0o600); err != nil {
		return err
	}
	return m.Chown("homestack-control", m.ControlEnv)
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

func (m *Manager) waitForHealth(ctx context.Context) error {
	client := m.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	deadline := m.Now().Add(30 * time.Second)
	for m.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:18443/api/health", nil)
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
	return errors.New("Control 健康检查超时")
}

func atomicJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), mode)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".homestack-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func chownFiles(account string, paths ...string) error {
	userAccount, err := user.Lookup(account)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(userAccount.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(userAccount.Gid)
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

func runWhitelistedCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	joined := strings.Join(arguments, "\x00")
	allowed := false
	if name == "/usr/local/bin/homestack-control" {
		allowed = joined == "configtest\x00--env-file\x00/etc/homestack/control.env"
	} else if name == "systemctl" {
		for _, candidate := range []string{
			"start\x00--no-block\x00homestack-setup-switch.service",
			"stop\x00homestack-setup.service", "stop\x00homestack-control.service",
			"enable\x00homestack-control.service",
			"restart\x00homestack-control.service", "restart\x00homestack-config-helper.service", "restart\x00homestack-setup.service",
			"enable\x00homestack-config-helper.service\x00homestack-setup.service",
			"disable\x00homestack-setup.service",
		} {
			if joined == candidate {
				allowed = true
				break
			}
		}
	} else if name == "systemd-run" {
		allowed = joined == "--unit=homestack-control-restart\x00--replace\x00--on-active=2s\x00--property=Type=oneshot\x00systemctl\x00restart\x00homestack-control.service"
	}
	if !allowed {
		return nil, fmt.Errorf("命令不在 Config Helper 白名单中: %s %s", name, strings.Join(arguments, " "))
	}
	command := exec.CommandContext(ctx, name, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return output, nil
}
