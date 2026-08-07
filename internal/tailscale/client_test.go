package tailscale

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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
			return []byte(`{"Web":{"443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`), nil
		case "funnel status --json":
			return []byte(`null`), nil
		case "serve --bg --https=19443 http://127.0.0.1:19444":
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

func TestEnsureServeRejectsPortAndFunnelConflict(t *testing.T) {
	for _, funnel := range []bool{false, true} {
		client := &Client{Binary: "tailscale", Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "serve" {
				if funnel {
					return []byte(`null`), nil
				}
				return []byte(`{"19443":"http://127.0.0.1:9999"}`), nil
			}
			return []byte(`{"19443":true}`), nil
		}}
		if err := client.EnsureServe(context.Background()); err == nil {
			t.Fatal("冲突配置必须被拒绝")
		}
	}
}
