package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Headscale interface {
	CreateSingleUseKey(ctx context.Context, email string) (string, error)
}

type HeadscaleCLI struct {
	Binary     string
	ConfigPath string
}

type headscaleUser struct {
	ID    uint64 `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func NewHeadscaleCLI(configPath string) (*HeadscaleCLI, error) {
	path, err := exec.LookPath("headscale")
	if err != nil {
		return nil, fmt.Errorf("未找到 Headscale: %w", err)
	}
	if configPath == "" {
		return nil, errors.New("必须明确指定 Headscale 配置路径")
	}
	return &HeadscaleCLI{Binary: path, ConfigPath: configPath}, nil
}

func (c *HeadscaleCLI) CreateSingleUseKey(ctx context.Context, email string) (string, error) {
	userID, err := c.findUserID(ctx, email)
	if err != nil {
		return "", err
	}
	output, err := c.run(ctx, "preauthkeys", "create", "--user", strconv.FormatUint(userID, 10), "--expiration", "10m")
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(output))
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return "", errors.New("Headscale 返回的单次预认证密钥格式无效")
	}
	return key, nil
}

func (c *HeadscaleCLI) findUserID(ctx context.Context, email string) (uint64, error) {
	if email == "" {
		return 0, errors.New("OIDC 身份缺少邮箱，无法匹配 Headscale 用户")
	}
	output, err := c.run(ctx, "--output", "json", "users", "list", "--email", email)
	if err != nil {
		return 0, err
	}
	var users []headscaleUser
	if err := json.Unmarshal(output, &users); err != nil {
		return 0, fmt.Errorf("解析 Headscale 用户列表失败: %w", err)
	}
	if len(users) != 1 || !strings.EqualFold(users[0].Email, email) {
		return 0, fmt.Errorf("Headscale 中邮箱 %q 必须且只能匹配一个用户", email)
	}
	return users[0].ID, nil
}

func (c *HeadscaleCLI) run(ctx context.Context, arguments ...string) ([]byte, error) {
	args := append([]string{"--config", c.ConfigPath}, arguments...)
	output, err := exec.CommandContext(ctx, c.Binary, args...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("Headscale 命令失败: %s", detail)
	}
	return output, nil
}
