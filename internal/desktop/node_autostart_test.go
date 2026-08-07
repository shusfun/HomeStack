package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentPinsTailscaleBinaryAndEscapesPaths(t *testing.T) {
	content := string(launchAgentContent("/Applications/Home & Stack.app/HomeStack", "/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/Users/test/Library/Logs/HomeStack/node.stdout.log", "/Users/test/Library/Logs/HomeStack/node.stderr.log"))
	for _, expected := range []string{"HOMESTACK_TAILSCALE_BINARY", "/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/Applications/Home &amp; Stack.app/HomeStack", "<string>--node</string>", "<key>TERM</key><string>xterm-256color</string>", "StandardOutPath", "node.stdout.log", "StandardErrorPath", "node.stderr.log"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("LaunchAgent 缺少 %q: %s", expected, content)
		}
	}
}

func TestPrepareNodeLogsRestrictsPermissions(t *testing.T) {
	home := t.TempDir()
	stdoutPath, stderrPath, err := prepareNodeLogs(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{stdoutPath, stderrPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("日志权限错误: %s", info.Mode().Perm())
		}
	}
	directoryInfo, err := os.Stat(filepath.Dir(stdoutPath))
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("日志目录权限错误: %s", directoryInfo.Mode().Perm())
	}
}

func TestSameUserFileChecksContentAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.plist")
	content := []byte("node")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if same, err := sameUserFile(path, content, 0o600); err != nil || !same {
		t.Fatalf("相同启动文件未被识别: same=%v err=%v", same, err)
	}
	if same, err := sameUserFile(path, []byte("changed"), 0o600); err != nil || same {
		t.Fatalf("不同启动文件被误判: same=%v err=%v", same, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if same, err := sameUserFile(path, content, 0o600); err != nil || same {
		t.Fatalf("宽权限启动文件被误判: same=%v err=%v", same, err)
	}
}
