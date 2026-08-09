//go:build linux

package agent

import "testing"

func TestLinuxKeyringNodeUsesLocalSystemManager(t *testing.T) {
	t.Setenv("HOMESTACK_NODE_PROFILE_SOURCE", "keyring")
	manager, err := newDefaultSystemManager()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.(*LocalSystemManager); !ok {
		t.Fatalf("Linux 桌面 Node 仍会访问 systemd helper: %T", manager)
	}
}

func TestLinuxSystemdNodeUsesHelper(t *testing.T) {
	t.Setenv("HOMESTACK_NODE_PROFILE_SOURCE", "systemd")
	manager, err := newDefaultSystemManager()
	if err != nil {
		t.Fatal(err)
	}
	if client, ok := manager.(*SystemClient); !ok || client.SocketPath != DefaultHelperSocket {
		t.Fatalf("Linux systemd Node 未使用 helper: %T", manager)
	}
}
