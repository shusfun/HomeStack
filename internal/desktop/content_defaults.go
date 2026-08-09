package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/wangshangbin/homestack/internal/protocol"
)

const (
	fileBrowserBaseURL = "http://127.0.0.1:19445"
	jellyfinBaseURL    = "http://127.0.0.1:19446"
)

func DiscoverDefaultContent() ([]protocol.SharedDirectory, []protocol.ModuleConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("读取用户主目录失败: %w", err)
	}
	directories, err := discoverDefaultDirectories(runtime.GOOS, home, os.Getenv, os.Stat, filepath.EvalSymlinks)
	if err != nil {
		return nil, nil, err
	}
	if len(directories) == 0 {
		return nil, nil, errors.New("未找到可共享的系统标准目录")
	}
	modules := []protocol.ModuleConfig{
		{ID: "filebrowser", Enabled: true, BaseURL: fileBrowserBaseURL, ReadOnly: true},
		{ID: "jellyfin", Enabled: true, BaseURL: jellyfinBaseURL, ReadOnly: true},
	}
	return directories, modules, nil
}

type statFile func(string) (os.FileInfo, error)
type resolveFile func(string) (string, error)

func discoverDefaultDirectories(goos, home string, getenv func(string) string, stat statFile, resolve resolveFile) ([]protocol.SharedDirectory, error) {
	home = filepath.Clean(home)
	if !filepath.IsAbs(home) {
		return nil, errors.New("用户主目录必须是绝对路径")
	}
	resolvedHome, err := resolve(home)
	if err != nil {
		return nil, fmt.Errorf("解析用户主目录失败: %w", err)
	}
	home = filepath.Clean(resolvedHome)
	type candidate struct{ id, name, path string }
	paths := []candidate{
		{id: "desktop", name: "桌面", path: filepath.Join(home, "Desktop")},
		{id: "documents", name: "文稿", path: filepath.Join(home, "Documents")},
		{id: "downloads", name: "下载", path: filepath.Join(home, "Downloads")},
		{id: "pictures", name: "图片", path: filepath.Join(home, "Pictures")},
		{id: "music", name: "音乐", path: filepath.Join(home, "Music")},
		{id: "videos", name: "影视", path: filepath.Join(home, map[bool]string{true: "Movies", false: "Videos"}[goos == "darwin"])},
	}
	if goos == "linux" {
		variables := []string{"XDG_DESKTOP_DIR", "XDG_DOCUMENTS_DIR", "XDG_DOWNLOAD_DIR", "XDG_PICTURES_DIR", "XDG_MUSIC_DIR", "XDG_VIDEOS_DIR"}
		for index, variable := range variables {
			if value := expandXDGPath(getenv(variable), home); value != "" {
				paths[index].path = value
			}
		}
	}
	seen := make(map[string]struct{}, len(paths))
	result := make([]protocol.SharedDirectory, 0, len(paths))
	for _, item := range paths {
		info, err := stat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("检查默认目录 %s 失败: %w", item.name, err)
		}
		if !info.IsDir() || strings.HasPrefix(filepath.Base(item.path), ".") {
			continue
		}
		resolved, err := resolve(item.path)
		if err != nil {
			return nil, fmt.Errorf("解析默认目录 %s 失败: %w", item.name, err)
		}
		resolved = filepath.Clean(resolved)
		if !filepath.IsAbs(resolved) {
			return nil, fmt.Errorf("默认目录 %s 不是绝对路径", item.name)
		}
		if !safeDefaultDirectory(goos, home, resolved) {
			continue
		}
		key := resolved
		if goos == "windows" {
			key = strings.ToLower(key)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, protocol.SharedDirectory{ID: item.id, Name: item.name, Path: resolved})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func safeDefaultDirectory(goos, home, candidate string) bool {
	normalize := func(value string) string {
		value = filepath.Clean(value)
		if goos == "windows" {
			return strings.ToLower(value)
		}
		return value
	}
	candidate, home = normalize(candidate), normalize(home)
	if candidate == home || candidate == normalize(filepath.VolumeName(candidate)+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.FieldsFunc(candidate, func(value rune) bool { return value == '/' || value == '\\' }) {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	if relative, err := filepath.Rel(home, candidate); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	if goos == "windows" {
		relative := strings.TrimPrefix(candidate, normalize(filepath.VolumeName(candidate)+string(filepath.Separator)))
		first := strings.SplitN(relative, string(filepath.Separator), 2)[0]
		return first != "windows" && first != "program files" && first != "program files (x86)" && first != "programdata"
	}
	lowerCandidate := strings.ToLower(candidate)
	for _, protected := range []string{"/bin", "/dev", "/etc", "/library", "/private", "/proc", "/sbin", "/system", "/usr", "/var"} {
		if lowerCandidate == protected || strings.HasPrefix(lowerCandidate, protected+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

func expandXDGPath(value, home string) string {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "$HOME", home)
	value = strings.ReplaceAll(value, "${HOME}", home)
	if !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}
