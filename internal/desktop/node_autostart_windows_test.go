//go:build windows

package desktop

import "testing"

func TestWindowsNodeCommandQuotesAbsolutePath(t *testing.T) {
	command, err := windowsNodeCommand(`C:\Users\test user\AppData\Local\Programs\HomeStack\HomeStack.exe`)
	if err != nil {
		t.Fatal(err)
	}
	expected := `"C:\Users\test user\AppData\Local\Programs\HomeStack\HomeStack.exe" --node`
	if command != expected {
		t.Fatalf("Windows Node 自启动命令错误: %q", command)
	}
}

func TestWindowsNodeCommandRejectsRelativePath(t *testing.T) {
	if _, err := windowsNodeCommand(`HomeStack.exe`); err == nil {
		t.Fatal("相对路径未被拒绝")
	}
}

func TestWindowsNodeCommandRejectsQuote(t *testing.T) {
	if _, err := windowsNodeCommand(`C:\Home"Stack\HomeStack.exe`); err == nil {
		t.Fatal("包含双引号的路径未被拒绝")
	}
}
