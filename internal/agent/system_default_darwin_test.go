//go:build darwin

package agent

import "testing"

func TestDarwinDefaultSystemManagerDoesNotUseLinuxHelper(t *testing.T) {
	manager, err := newDefaultSystemManager()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.(*LocalSystemManager); !ok {
		t.Fatalf("macOS 默认系统管理器仍会访问 Linux helper: %T", manager)
	}
}
