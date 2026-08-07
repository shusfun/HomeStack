package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wangshangbin/homestack/internal/protocol"
)

func TestFileServiceRejectsTraversalSymlinkAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "allowed.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	service := NewFileService([]protocol.SharedDirectory{{ID: "docs", Name: "文档", Path: root}})
	if _, _, err := service.ResolveFile("/docs/allowed.txt"); err != nil {
		t.Fatalf("普通文件被拒绝: %v", err)
	}
	for _, path := range []string{"/docs/../secret.txt", "/docs/escape"} {
		if _, _, err := service.ResolveFile(path); err == nil {
			t.Fatalf("越界路径 %q 未被拒绝", path)
		}
	}
}
