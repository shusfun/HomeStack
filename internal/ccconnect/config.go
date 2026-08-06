package ccconnect

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var projectNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Project struct {
	Name      string
	WorkDir   string
	BotID     string
	BotSecret string
	AllowFrom []string
	AdminFrom []string
}

type configFile struct {
	Language string          `toml:"language"`
	Projects []projectConfig `toml:"projects"`
}

type projectConfig struct {
	Name             string           `toml:"name"`
	AdminFrom        string           `toml:"admin_from"`
	DisabledCommands []string         `toml:"disabled_commands"`
	Agent            agentConfig      `toml:"agent"`
	Platforms        []platformConfig `toml:"platforms"`
}

type agentConfig struct {
	Type    string       `toml:"type"`
	Options agentOptions `toml:"options"`
}

type agentOptions struct {
	WorkDir      string `toml:"work_dir"`
	Mode         string `toml:"mode"`
	Backend      string `toml:"backend"`
	AppServerURL string `toml:"app_server_url"`
}

type platformConfig struct {
	Type    string          `toml:"type"`
	Options platformOptions `toml:"options"`
}

type platformOptions struct {
	Mode      string `toml:"mode"`
	BotID     string `toml:"bot_id"`
	BotSecret string `toml:"bot_secret"`
	AllowFrom string `toml:"allow_from"`
}

func WriteConfig(path string, projects []Project) error {
	if path == "" {
		return errors.New("cc-connect 配置路径不能为空")
	}
	if len(projects) == 0 {
		return errors.New("至少需要配置一个 cc-connect 项目")
	}
	config := configFile{Language: "zh", Projects: make([]projectConfig, 0, len(projects))}
	seen := map[string]struct{}{}
	for _, project := range projects {
		if err := validateProject(project); err != nil {
			return err
		}
		if _, exists := seen[project.Name]; exists {
			return fmt.Errorf("cc-connect 项目名称重复: %s", project.Name)
		}
		seen[project.Name] = struct{}{}
		config.Projects = append(config.Projects, projectConfig{
			Name:             project.Name,
			AdminFrom:        strings.Join(project.AdminFrom, ","),
			DisabledCommands: []string{"restart", "upgrade", "cron", "shell", "dir"},
			Agent: agentConfig{Type: "codex", Options: agentOptions{
				WorkDir: project.WorkDir, Mode: "suggest", Backend: "app_server", AppServerURL: "stdio",
			}},
			Platforms: []platformConfig{{Type: "wecom", Options: platformOptions{
				Mode: "websocket", BotID: project.BotID, BotSecret: project.BotSecret, AllowFrom: strings.Join(project.AllowFrom, ","),
			}}},
		})
	}
	data, err := toml.Marshal(config)
	if err != nil {
		return fmt.Errorf("编码 cc-connect TOML 失败: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建 cc-connect 配置目录失败: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".cc-connect-*")
	if err != nil {
		return fmt.Errorf("创建 cc-connect 临时配置失败: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置 cc-connect 配置权限失败: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入 cc-connect 配置失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步 cc-connect 配置失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭 cc-connect 配置失败: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("替换 cc-connect 配置失败: %w", err)
	}
	return nil
}

func validateProject(project Project) error {
	if !projectNamePattern.MatchString(project.Name) {
		return fmt.Errorf("cc-connect 项目名称 %q 格式无效", project.Name)
	}
	if !filepath.IsAbs(project.WorkDir) {
		return errors.New("cc-connect work_dir 必须是绝对路径")
	}
	info, err := os.Stat(project.WorkDir)
	if err != nil {
		return fmt.Errorf("读取 cc-connect work_dir 失败: %w", err)
	}
	if !info.IsDir() {
		return errors.New("cc-connect work_dir 必须是目录")
	}
	if strings.TrimSpace(project.BotID) == "" || strings.TrimSpace(project.BotSecret) == "" {
		return errors.New("企业微信 bot_id 和 bot_secret 必须明确填写")
	}
	if err := validateUsers("allow_from", project.AllowFrom); err != nil {
		return err
	}
	if err := validateUsers("admin_from", project.AdminFrom); err != nil {
		return err
	}
	return nil
}

func validateUsers(field string, users []string) error {
	if len(users) == 0 {
		return fmt.Errorf("%s 必须明确填写", field)
	}
	for _, user := range users {
		if strings.TrimSpace(user) == "" || strings.TrimSpace(user) == "*" {
			return fmt.Errorf("%s 不允许为空或使用通配符", field)
		}
	}
	return nil
}
