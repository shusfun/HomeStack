//go:build linux

package helper

import (
	"context"
	"strings"
	"testing"
)

func TestServiceAndActionWhitelistRejectsUnknownValues(t *testing.T) {
	server := &Server{}
	for _, test := range []struct{ service, action string }{
		{"tailscale", "stop"},
		{"homestack-agent", "start"},
		{"arbitrary.service", "restart"},
		{"jellyfin", "reload-or-restart"},
	} {
		if err := server.action(context.Background(), test.service, test.action); err == nil {
			t.Fatalf("不在白名单的服务动作必须被拒绝: %s/%s", test.service, test.action)
		}
	}
}

func TestLogBoundsAreValidatedBeforeJournalctl(t *testing.T) {
	server := &Server{}
	if _, err := server.logs(context.Background(), "unknown", 100, ""); err == nil {
		t.Fatal("未知日志服务必须被拒绝")
	}
	if _, err := server.logs(context.Background(), "tailscale", 501, ""); err == nil {
		t.Fatal("超过 500 行的日志请求必须被拒绝")
	}
	if _, err := server.logs(context.Background(), "tailscale", 100, "bad cursor"); err == nil {
		t.Fatal("包含空白的 journal cursor 必须被拒绝")
	}
}

func TestLogSecretsAreRedacted(t *testing.T) {
	message := "Authorization: Bearer-value token=abc password: secret cookie=session"
	redacted := secretPattern.ReplaceAllString(message, "$1$2[REDACTED]")
	for _, secret := range []string{"Bearer-value", "abc", "secret", "session"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("日志脱敏后仍包含敏感值 %q: %s", secret, redacted)
		}
	}
}
