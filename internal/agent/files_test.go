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
	if err := os.WriteFile(filepath.Join(root, ".hidden.txt"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/docs/../secret.txt", "/docs/escape", "/docs/.hidden.txt"} {
		if _, _, err := service.ResolveFile(path); err == nil {
			t.Fatalf("越界路径 %q 未被拒绝", path)
		}
	}
}

func TestFileServiceSearchSkipsHiddenFiles(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"movie-one.mp4": "video", ".movie-secret.mp4": "secret", "note.txt": "text"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := NewFileService([]protocol.SharedDirectory{{ID: "videos", Name: "影视", Path: root}})
	results, err := service.Search("movie", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "/videos/movie-one.mp4" {
		t.Fatalf("搜索结果包含隐藏文件或路径错误: %+v", results)
	}
}

func TestFileServiceRootUsesDirectoryIDAsVirtualPath(t *testing.T) {
	service := NewFileService([]protocol.SharedDirectory{{ID: "downloads", Name: "下载", Path: t.TempDir()}})
	resource, err := service.List("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Folders) != 1 {
		t.Fatalf("根目录数量错误: %+v", resource.Folders)
	}
	folder := resource.Folders[0]
	if folder.Name != "下载" || folder.Path != "/downloads" {
		t.Fatalf("显示名称与虚拟路径未正确解耦: %+v", folder)
	}
	if _, err := service.List(folder.Path); err != nil {
		t.Fatalf("服务端返回的虚拟路径无法访问: %v", err)
	}
}
