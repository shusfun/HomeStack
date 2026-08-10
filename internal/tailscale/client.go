package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const ExpectedVersion = "1.102.2"
const BinaryEnvironment = "HOMESTACK_TAILSCALE_BINARY"

type Runner func(context.Context, string, ...string) ([]byte, error)

type Client struct {
	Binary string
	Run    Runner
}

type Status struct {
	Online      bool
	TailscaleIP string
	MagicDNS    string
	Connection  string
}

type rawStatus struct {
	BackendState string   `json:"BackendState"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Self         struct {
		Online  bool   `json:"Online"`
		DNSName string `json:"DNSName"`
	} `json:"Self"`
	Peer map[string]struct {
		Active  bool   `json:"Active"`
		CurAddr string `json:"CurAddr"`
		Relay   string `json:"Relay"`
	} `json:"Peer"`
}

type rawServeConfig struct {
	TCP map[string]json.RawMessage `json:"TCP"`
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
	AllowFunnel map[string]bool `json:"AllowFunnel"`
}

func New() (*Client, error) {
	path, err := ResolveBinary()
	if err != nil {
		return nil, err
	}
	return &Client{Binary: path, Run: runCommand}, nil
}

func ResolveBinary() (string, error) {
	binary := strings.TrimSpace(os.Getenv(BinaryEnvironment))
	if binary != "" && !filepath.IsAbs(binary) {
		return "", errors.New("HOMESTACK_TAILSCALE_BINARY 必须是绝对路径")
	}
	if binary != "" {
		path, err := exec.LookPath(binary)
		if err != nil {
			return "", fmt.Errorf("显式 Tailscale CLI 不可执行: %w", err)
		}
		return path, nil
	}
	if runtime.GOOS == "darwin" {
		const appBinary = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
		if path, err := exec.LookPath(appBinary); err == nil {
			return path, nil
		}
	}
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path, nil
	}
	if runtime.GOOS == "darwin" {
		for _, candidate := range []string{"/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale"} {
			if path, err := exec.LookPath(candidate); err == nil {
				return path, nil
			}
		}
	}
	return "", errors.New("未找到官方 Tailscale CLI")
}

func (c *Client) VerifyVersion(ctx context.Context) error {
	output, err := c.run(ctx, "version", "--json")
	if err != nil {
		return fmt.Errorf("读取 Tailscale 版本失败: %w", err)
	}
	var version struct {
		Short string `json:"short"`
		Long  string `json:"long"`
	}
	if err := json.Unmarshal(output, &version); err != nil {
		return fmt.Errorf("解析 Tailscale 版本失败: %w", err)
	}
	actual := strings.TrimPrefix(version.Short, "v")
	if actual == "" {
		actual = strings.TrimPrefix(version.Long, "v")
	}
	if actual != ExpectedVersion {
		return fmt.Errorf("Tailscale 版本不匹配: 需要 %s，检测到 %s", ExpectedVersion, actual)
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := c.run(commandCtx, "status", "--json")
	if err != nil {
		return Status{}, fmt.Errorf("读取 Tailscale 状态失败: %w", err)
	}
	var raw rawStatus
	if err := json.Unmarshal(output, &raw); err != nil {
		return Status{}, fmt.Errorf("解析 Tailscale 状态失败: %w", err)
	}
	if raw.BackendState != "Running" || !raw.Self.Online {
		return Status{}, errors.New("Tailscale 尚未登录或未处于 Running 状态")
	}
	if len(raw.TailscaleIPs) == 0 || strings.TrimSpace(raw.Self.DNSName) == "" {
		return Status{}, errors.New("Tailscale 状态缺少 IP 或 MagicDNS 名称")
	}
	status := Status{Online: true, TailscaleIP: raw.TailscaleIPs[0], MagicDNS: strings.TrimSuffix(raw.Self.DNSName, "."), Connection: "中继"}
	for _, peer := range raw.Peer {
		if peer.Active && peer.CurAddr != "" {
			status.Connection = "直连"
			break
		}
	}
	return status, nil
}

func (c *Client) EnsureServe(ctx context.Context) error {
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	serve, err := c.run(commandCtx, "serve", "status", "--json")
	if err != nil {
		return fmt.Errorf("读取 Tailscale Serve 配置失败: %w", err)
	}
	var config rawServeConfig
	if err := json.Unmarshal(serve, &config); err != nil {
		return fmt.Errorf("解析 Tailscale Serve 配置失败: %w", err)
	}
	if config.funnelEnabled("19443") {
		return errors.New("Tailscale Funnel 已占用 19443，拒绝暴露 HomeStack Node")
	}
	if _, occupied := config.TCP["19443"]; occupied {
		if config.proxy("19443", "/") == "http://127.0.0.1:19444" {
			return nil
		}
		return errors.New("Tailscale Serve 端口 19443 已被其他服务占用")
	}
	if _, err := c.run(commandCtx, "serve", "--yes", "--bg", "--https=19443", "http://127.0.0.1:19444"); err != nil {
		return fmt.Errorf("添加 HomeStack Serve 映射失败: %w", err)
	}
	return nil
}

func (c rawServeConfig) funnelEnabled(port string) bool {
	for hostPort, enabled := range c.AllowFunnel {
		_, candidate, err := net.SplitHostPort(hostPort)
		if err == nil && candidate == port && enabled {
			return true
		}
	}
	return false
}

func (c rawServeConfig) proxy(port, path string) string {
	for hostPort, web := range c.Web {
		_, candidate, err := net.SplitHostPort(hostPort)
		if err == nil && candidate == port {
			return web.Handlers[path].Proxy
		}
	}
	return ""
}

func (c *Client) run(ctx context.Context, arguments ...string) ([]byte, error) {
	runner := c.Run
	if runner == nil {
		runner = runCommand
	}
	return runner(ctx, c.Binary, arguments...)
}

func runCommand(ctx context.Context, binary string, arguments ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, binary, arguments...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	return output, nil
}
