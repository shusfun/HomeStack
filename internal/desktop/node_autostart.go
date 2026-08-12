package desktop

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/wangshangbin/homestack/internal/tailscale"
)

const nodeLabel = "dev.homestack.node"

var nodeAutostartMu sync.Mutex

func ConfigureNodeEnvironment() error {
	stateDir, err := nodeStateDirectory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("创建 Node 状态目录失败: %w", err)
	}
	for name, value := range map[string]string{"HOMESTACK_AGENT_ADDR": "127.0.0.1:19444", "HOMESTACK_AGENT_STATE_DIR": stateDir, "HOMESTACK_NODE_PROFILE_SOURCE": "keyring"} {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}

func ConfigureNodeAutostart() error {
	nodeAutostartMu.Lock()
	defer nodeAutostartMu.Unlock()
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 HomeStack App 失败: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	tailscaleBinary, err := tailscale.ResolveBinary()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return configureLaunchAgent(executable, tailscaleBinary)
	case "linux":
		return configureSystemdUser(executable)
	case "windows":
		return configureWindowsStartup(executable)
	default:
		return fmt.Errorf("不支持的 Node 后台启动平台: %s", runtime.GOOS)
	}
}

func RepairNodeAutostart() error {
	if runtime.GOOS == "darwin" {
		return ConfigureNodeAutostart()
	}
	if runtime.GOOS == "windows" {
		if err := ConfigureNodeAutostart(); err != nil {
			return err
		}
		return RestartNode()
	}
	return nil
}

func RestartNode() error {
	nodeAutostartMu.Lock()
	defer nodeAutostartMu.Unlock()
	switch runtime.GOOS {
	case "darwin":
		target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + nodeLabel
		return runStartupCommand("launchctl", "kickstart", "-k", target)
	case "linux":
		return runStartupCommand("systemctl", "--user", "restart", "homestack-node.service")
	case "windows":
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("定位 HomeStack App 失败: %w", err)
		}
		executable, err = filepath.Abs(executable)
		if err != nil {
			return err
		}
		return restartWindowsNode(executable)
	default:
		return fmt.Errorf("不支持的 Node 重启平台: %s", runtime.GOOS)
	}
}

func configureLaunchAgent(executable, tailscaleBinary string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	directory := filepath.Join(home, "Library", "LaunchAgents")
	path := filepath.Join(directory, nodeLabel+".plist")
	stdoutPath, stderrPath, err := prepareNodeLogs(home)
	if err != nil {
		return err
	}
	executableFingerprint, err := fileSHA256(executable)
	if err != nil {
		return fmt.Errorf("计算 HomeStack App 指纹失败: %w", err)
	}
	content := launchAgentContent(executable, executableFingerprint, tailscaleBinary, stdoutPath, stderrPath)
	unchanged, err := sameUserFile(path, content, 0o600)
	if err != nil {
		return err
	}
	if !unchanged {
		if err := atomicUserFile(path, content, 0o600); err != nil {
			return err
		}
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	target := domain + "/" + nodeLabel
	if exec.Command("launchctl", "print", target).Run() == nil {
		if unchanged {
			return nil
		}
		if err := runStartupCommand("launchctl", "bootout", target); err != nil {
			return err
		}
	}
	return runStartupCommand("launchctl", "bootstrap", domain, path)
}

func launchAgentContent(executable, executableFingerprint, tailscaleBinary, stdoutPath, stderrPath string) []byte {
	return []byte(fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"https://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>%s</string><string>--node</string></array><key>EnvironmentVariables</key><dict><key>HOMESTACK_NODE_EXECUTABLE_SHA256</key><string>%s</string><key>%s</key><string>%s</string><key>TERM</key><string>xterm-256color</string></dict><key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string><key>RunAtLoad</key><true/><key>KeepAlive</key><true/></dict></plist>\n", nodeLabel, html.EscapeString(executable), html.EscapeString(executableFingerprint), tailscale.BinaryEnvironment, html.EscapeString(tailscaleBinary), html.EscapeString(stdoutPath), html.EscapeString(stderrPath)))
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func prepareNodeLogs(home string) (string, string, error) {
	directory := filepath.Join(home, "Library", "Logs", "HomeStack")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", "", fmt.Errorf("创建 Node 日志目录失败: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", "", fmt.Errorf("限制 Node 日志目录权限失败: %w", err)
	}
	paths := []string{filepath.Join(directory, "node.stdout.log"), filepath.Join(directory, "node.stderr.log")}
	for _, path := range paths {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return "", "", fmt.Errorf("创建 Node 日志失败: %w", err)
		}
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return "", "", fmt.Errorf("限制 Node 日志权限失败: %w", err)
		}
		if err := file.Close(); err != nil {
			return "", "", fmt.Errorf("关闭 Node 日志失败: %w", err)
		}
	}
	return paths[0], paths[1], nil
}

func sameUserFile(path string, expected []byte, mode os.FileMode) (bool, error) {
	current, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return bytes.Equal(current, expected) && info.Mode().Perm() == mode.Perm(), nil
}

func configureSystemdUser(executable string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".config", "systemd", "user", "homestack-node.service")
	content := "[Unit]\nDescription=HomeStack desktop node\nAfter=network-online.target tailscaled.service\nWants=network-online.target\n\n[Service]\nType=simple\nExecStart=" + strconv.Quote(executable) + " --node\nRestart=on-failure\nRestartSec=5s\nNoNewPrivileges=true\nPrivateTmp=true\n\n[Install]\nWantedBy=default.target\n"
	if err := atomicUserFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	if err := runStartupCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return runStartupCommand("systemctl", "--user", "enable", "--now", "homestack-node.service")
}

func nodeStateDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		return filepath.Join(home, ".local", "state", "homestack"), nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "HomeStack", "node"), nil
}

func atomicUserFile(path string, data []byte, mode os.FileMode) error {
	if !filepath.IsAbs(path) {
		return errors.New("用户配置路径必须是绝对路径")
	}
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

func runStartupCommand(name string, arguments ...string) error {
	output, err := exec.Command(name, arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s 失败: %w: %s", name, strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
