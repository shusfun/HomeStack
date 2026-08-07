package desktop

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const nodeLabel = "dev.homestack.node"

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
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 HomeStack App 失败: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return configureLaunchAgent(executable)
	case "linux":
		return configureSystemdUser(executable)
	case "windows":
		return configureWindowsStartup(executable)
	default:
		return fmt.Errorf("不支持的 Node 后台启动平台: %s", runtime.GOOS)
	}
}

func configureLaunchAgent(executable string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	directory := filepath.Join(home, "Library", "LaunchAgents")
	path := filepath.Join(directory, nodeLabel+".plist")
	content := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"https://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>%s</string><string>--node</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/></dict></plist>\n", nodeLabel, html.EscapeString(executable))
	if err := atomicUserFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	target := domain + "/" + nodeLabel
	if exec.Command("launchctl", "print", target).Run() == nil {
		return runStartupCommand("launchctl", "kickstart", "-k", target)
	}
	return runStartupCommand("launchctl", "bootstrap", domain, path)
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

func configureWindowsStartup(executable string) error {
	command := `"` + strings.ReplaceAll(executable, `"`, `\"`) + `" --node`
	if err := runStartupCommand("schtasks", "/Create", "/SC", "ONLOGON", "/TN", "HomeStackNode", "/TR", command, "/F"); err != nil {
		return err
	}
	return runStartupCommand("schtasks", "/Run", "/TN", "HomeStackNode")
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
