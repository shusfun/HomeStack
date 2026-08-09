package desktop

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverDefaultDirectoriesUsesExistingCanonicalPaths(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"Desktop", "Downloads", "Movies"} {
		if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	directories, err := discoverDefaultDirectories("darwin", home, func(string) string { return "" }, os.Stat, filepath.EvalSymlinks)
	if err != nil {
		t.Fatal(err)
	}
	if len(directories) != 3 || directories[0].ID != "desktop" || directories[1].ID != "downloads" || directories[2].ID != "videos" {
		t.Fatalf("默认目录不符合预期: %+v", directories)
	}
}

func TestDiscoverDefaultDirectoriesUsesLinuxXDGAndDeduplicates(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, "共享")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	getenv := func(name string) string {
		if name == "XDG_DOWNLOAD_DIR" || name == "XDG_VIDEOS_DIR" {
			return "$HOME/共享"
		}
		return ""
	}
	directories, err := discoverDefaultDirectories("linux", home, getenv, os.Stat, filepath.EvalSymlinks)
	if err != nil {
		t.Fatal(err)
	}
	resolvedShared, err := filepath.EvalSymlinks(shared)
	if err != nil {
		t.Fatal(err)
	}
	if len(directories) != 1 || directories[0].Path != resolvedShared {
		t.Fatalf("XDG 目录未规范化去重: %+v", directories)
	}
}

func TestDiscoverDefaultDirectoriesReturnsRealStatError(t *testing.T) {
	home := t.TempDir()
	permissionErr := &fs.PathError{Op: "stat", Path: filepath.Join(home, "Desktop"), Err: fs.ErrPermission}
	_, err := discoverDefaultDirectories("windows", home, func(string) string { return "" }, func(string) (os.FileInfo, error) {
		return nil, permissionErr
	}, filepath.EvalSymlinks)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("真实目录错误被隐藏: %v", err)
	}
}

func TestDiscoverDefaultDirectoriesRejectsHomeHiddenAndSystemTargets(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"Desktop", "Downloads", "Movies"} {
		if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	resolve := func(path string) (string, error) {
		switch filepath.Base(path) {
		case "Desktop":
			return home, nil
		case "Downloads":
			return filepath.Join(home, ".private"), nil
		case "Movies":
			return "/System/Library", nil
		default:
			return path, nil
		}
	}
	directories, err := discoverDefaultDirectories("darwin", home, func(string) string { return "" }, os.Stat, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(directories) != 0 {
		t.Fatalf("危险默认目录未被排除: %+v", directories)
	}
}
