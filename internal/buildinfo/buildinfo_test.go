package buildinfo

import "testing"

func TestRequested(t *testing.T) {
	for _, argument := range []string{"version", "--version", "-v"} {
		if !Requested([]string{argument}) {
			t.Fatalf("应识别版本参数 %q", argument)
		}
	}
	if Requested(nil) || Requested([]string{"--version", "extra"}) || Requested([]string{"--help"}) {
		t.Fatal("不应把其他参数识别为版本请求")
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
