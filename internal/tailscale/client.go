package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const ExpectedVersion = "1.102.2"

const selfHostedDERPRegion = "homestack"

type Client struct {
	Binary string
}

type Status struct {
	Online     bool
	TailnetIP  string
	Connection string
	DERPRegion string
}

type rawStatus struct {
	BackendState string   `json:"BackendState"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Self         struct {
		Online bool `json:"Online"`
	} `json:"Self"`
	Peer map[string]struct {
		Active  bool   `json:"Active"`
		CurAddr string `json:"CurAddr"`
		Relay   string `json:"Relay"`
	} `json:"Peer"`
}

type derpMap struct {
	Regions map[int]struct {
		RegionCode string
	} `json:"Regions"`
}

func New() (*Client, error) {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return nil, fmt.Errorf("未找到官方 Tailscale 客户端: %w", err)
	}
	return &Client{Binary: path}, nil
}

func (c *Client) VerifyVersion(ctx context.Context) error {
	output, err := exec.CommandContext(ctx, c.Binary, "version", "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("读取 Tailscale 版本失败: %s", commandDetail(err, output))
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

func (c *Client) Up(ctx context.Context, loginServer, authKey string) error {
	if loginServer == "" || authKey == "" {
		return errors.New("Tailscale 入网参数不完整")
	}
	command := exec.CommandContext(ctx, c.Binary, "up", "--login-server="+loginServer, "--auth-key="+authKey, "--accept-routes=false", "--accept-dns=true")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Tailscale 入网失败: %s", commandDetail(err, output))
	}
	return nil
}

func (c *Client) VerifyNetworkPolicy(ctx context.Context) error {
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, c.Binary, "debug", "derp-map").CombinedOutput()
	if err != nil {
		return fmt.Errorf("读取 Tailscale DERP map 失败: %s", commandDetail(err, output))
	}
	return validateDERPMap(output)
}

func validateDERPMap(data []byte) error {
	var current derpMap
	if err := json.Unmarshal(data, &current); err != nil {
		return fmt.Errorf("解析 Tailscale DERP map 失败: %w", err)
	}
	if len(current.Regions) != 1 {
		return fmt.Errorf("DERP map 必须且只能包含一个自有区域，实际包含 %d 个", len(current.Regions))
	}
	for _, region := range current.Regions {
		if region.RegionCode != selfHostedDERPRegion {
			return fmt.Errorf("检测到非自有 DERP 区域 %q", region.RegionCode)
		}
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, c.Binary, "status", "--json").CombinedOutput()
	if err != nil {
		return Status{}, fmt.Errorf("读取 Tailscale 状态失败: %s", commandDetail(err, output))
	}
	var raw rawStatus
	if err := json.Unmarshal(output, &raw); err != nil {
		return Status{}, fmt.Errorf("解析 Tailscale 状态失败: %w", err)
	}
	status := Status{Online: raw.Self.Online && raw.BackendState == "Running", Connection: "未连接"}
	if len(raw.TailscaleIPs) > 0 {
		status.TailnetIP = raw.TailscaleIPs[0]
	}
	for _, peer := range raw.Peer {
		if !peer.Active {
			continue
		}
		if peer.CurAddr != "" {
			status.Connection = "直连"
			status.DERPRegion = ""
			break
		}
		if peer.Relay != "" && status.Connection != "直连" {
			if peer.Relay != selfHostedDERPRegion {
				return Status{}, fmt.Errorf("检测到非自有 DERP 中继 %q", peer.Relay)
			}
			status.Connection = "自有中继"
			status.DERPRegion = peer.Relay
		}
	}
	return status, nil
}

func commandDetail(err error, output []byte) string {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return err.Error()
	}
	return err.Error() + ": " + detail
}
