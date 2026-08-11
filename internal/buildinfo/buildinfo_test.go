package buildinfo

import (
	"encoding/json"
	"testing"

	"github.com/wangshangbin/homestack/internal/releaseproxy"
)

func TestRequested(t *testing.T) {
	for _, argument := range []string{"version", "--version", "-v", "--version-json"} {
		if !Requested([]string{argument}) {
			t.Fatalf("应识别版本参数 %q", argument)
		}
	}
	if Requested(nil) || Requested([]string{"--version", "extra"}) || Requested([]string{"--help"}) {
		t.Fatal("不应把其他参数识别为版本请求")
	}
}

func TestVersionJSON(t *testing.T) {
	oldVersion := Version
	t.Cleanup(func() { Version = oldVersion })
	Version = "1.2.3"
	var value struct {
		Name, Version, GOOS, GOARCH string
	}
	if err := json.Unmarshal([]byte(Output("homestack-agent", []string{"--version-json"})), &value); err != nil {
		t.Fatal(err)
	}
	if value.Name != "homestack-agent" || value.Version != "1.2.3" || value.GOOS == "" || value.GOARCH == "" {
		t.Fatalf("版本 JSON 不完整: %+v", value)
	}
}

func TestString(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldVersion, oldCommit, oldDate })
	Version, Commit, Date = "1.2.3", "abc123", "2026-08-06T00:00:00Z"
	if got := String("homestack-agent"); got != "homestack-agent 1.2.3 (commit abc123, built 2026-08-06T00:00:00Z)" {
		t.Fatalf("版本输出不符合契约: %s", got)
	}
}

func TestReleaseManifestsUseFixedProxy(t *testing.T) {
	for _, endpoint := range []string{UpdateManifestURL, AgentUpdateManifestURL, ComponentManifestURL} {
		if !releaseproxy.IsProxyURL(endpoint) {
			t.Fatalf("发布清单未使用固定代理: %s", endpoint)
		}
	}
}
