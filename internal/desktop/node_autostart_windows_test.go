//go:build windows

package desktop

import (
	"strings"
	"testing"
)

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

func TestManagedWindowsNodeExecutableAcceptsCurrentAndUpdateBackup(t *testing.T) {
	expected := `C:\Users\test\AppData\Local\Programs\HomeStack\HomeStack.exe`
	for _, actual := range []string{
		expected,
		`c:\users\test\appdata\local\programs\homestack\homestack.exe`,
		expected + `.old.1786523980377557500`,
		`C:\Users\测试用户\AppData\Local\Programs\HomeStack\HomeStack.exe.old.1786523980377557500`,
	} {
		actualExpected := expected
		if strings.Contains(actual, `测试用户`) {
			actualExpected = `C:\Users\测试用户\AppData\Local\Programs\HomeStack\HomeStack.exe`
		}
		if !isManagedWindowsNodeExecutable(actualExpected, actual) {
			t.Fatalf("合法 HomeStack Node 进程路径被拒绝: %s", actual)
		}
	}
}

func TestManagedWindowsNodeExecutableRejectsOtherProcesses(t *testing.T) {
	expected := `C:\Users\test\AppData\Local\Programs\HomeStack\HomeStack.exe`
	for _, actual := range []string{
		`C:\Other\HomeStack.exe`,
		expected + `.old.`,
		expected + `.old.invalid`,
		expected + `.old.1786523980377557500.exe`,
		expected + `.backup.1786523980377557500`,
	} {
		if isManagedWindowsNodeExecutable(expected, actual) {
			t.Fatalf("非托管进程路径被错误接管: %s", actual)
		}
	}
}
