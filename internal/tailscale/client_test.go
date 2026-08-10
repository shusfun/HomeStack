package tailscale

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestNewUsesExplicitAbsoluteBinary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(BinaryEnvironment, executable)
	client, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if client.Binary != executable {
		t.Fatalf("未使用显式 Tailscale 路径: %s", client.Binary)
	}
}

func TestNewRejectsRelativeExplicitBinary(t *testing.T) {
	t.Setenv(BinaryEnvironment, "tailscale")
	if _, err := New(); err == nil || !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("相对路径未被拒绝: %v", err)
	}
}

func TestStatusRequiresLoggedInMagicDNS(t *testing.T) {
	client := &Client{Binary: "tailscale", Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "status --json" {
			return nil, errors.New("意外命令")
		}
		return []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.8"],"Self":{"Online":true,"DNSName":"mac.tail-name.ts.net."}}`), nil
	}}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.TailscaleIP != "100.64.0.8" || status.MagicDNS != "mac.tail-name.ts.net" {
		t.Fatalf("状态解析错误: %+v", status)
	}
}

func TestEnsureServePreservesConfigurationAndNeverRunsUp(t *testing.T) {
	var calls []string
	client := &Client{Binary: "tailscale", Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		switch joined {
		case "serve status --json":
			return []byte(`{"TCP":{"443":{"HTTPS":true}},"Web":{"host.tail-name.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`), nil
		case "serve --yes --bg --https=19443 http://127.0.0.1:19444":
			return nil, nil
		default:
			return nil, errors.New("意外命令: " + joined)
		}
	}}
	if err := client.EnsureServe(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "up ") {
			t.Fatalf("执行了禁止命令: %s", call)
		}
	}
}

func TestEnsureServeReusesExistingTailnetOnlyProxy(t *testing.T) {
	var calls []string
	client := &Client{Binary: "tailscale", Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte(`{"TCP":{"19443":{"HTTPS":true}},"Web":{"mac.tail-name.ts.net:19443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:19444"}}}}}`), nil
	}}
	if err := client.EnsureServe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "serve status --json" {
		t.Fatalf("复用现有 Serve 时执行了多余命令: %v", calls)
	}
}

func TestEnsureServeRejectsPortAndFunnelConflict(t *testing.T) {
	for _, funnel := range []bool{false, true} {
		client := &Client{Binary: "tailscale", Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if funnel {
				return []byte(`{"TCP":{"19443":{"HTTPS":true}},"Web":{"mac.tail-name.ts.net:19443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:19444"}}}},"AllowFunnel":{"mac.tail-name.ts.net:19443":true}}`), nil
			}
			return []byte(`{"TCP":{"19443":{"HTTPS":true}},"Web":{"mac.tail-name.ts.net:19443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}}}`), nil
		}}
		if err := client.EnsureServe(context.Background()); err == nil {
			t.Fatal("冲突配置必须被拒绝")
		}
	}
}
