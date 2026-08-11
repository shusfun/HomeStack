//go:build ignore

package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wangshangbin/homestack/internal/managed"
)

func TestComponentSourcesCoverDesktopPlatforms(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位组件锁定清单测试文件")
	}
	sources, err := readComponentLock(filepath.Join(filepath.Dir(filename), "components.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 18 {
		t.Fatalf("组件清单应包含 18 条平台记录，实际 %d", len(sources))
	}
	seen := make(map[string]managed.Artifact, len(sources))
	for _, artifact := range sources {
		seen[artifact.Component+"/"+artifact.Platform+"/"+artifact.Arch] = artifact
	}
	for _, platform := range []string{"darwin", "windows", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			for _, component := range []string{"filebrowser", "jellyfin", "jellyfin-ffmpeg"} {
				key := component + "/" + platform + "/" + arch
				if _, ok := seen[key]; !ok {
					t.Fatalf("组件清单缺少 %s", key)
				}
			}
		}
	}
	if seen["filebrowser/windows/amd64"].URL != seen["filebrowser/windows/arm64"].URL {
		t.Fatal("Windows ARM64 应复用官方 x64 FileBrowser 兼容资产")
	}
	for key, artifact := range seen {
		if artifact.Component == "filebrowser" && (artifact.Version != managed.FileBrowserVersion || !strings.Contains(artifact.URL, "/v1.5.1-stable/")) {
			t.Fatalf("FileBrowser 来源未固定到官方稳定版本: key=%s version=%s url=%s", key, artifact.Version, artifact.URL)
		}
	}
	for index := range sources {
		sources[index].URLs = candidateURLs(sources[index].URL)
	}
	if err := managed.ValidateManifest(managed.Manifest{SchemaVersion: managed.ManifestSchema, Artifacts: sources}); err != nil {
		t.Fatalf("锁定清单生成的组件清单无效: %v", err)
	}
}

func TestCandidateURLsUseApprovedPublicAccelerators(t *testing.T) {
	official := "https://github.com/example/project/releases/download/v1/file.zip"
	candidates := candidateURLs(official)
	if len(candidates) != 4 || candidates[3] != official {
		t.Fatalf("GitHub 候选源不完整: %v", candidates)
	}
	for _, host := range []string{"ghproxy.net", "ghfast.top", "gh-proxy.com"} {
		if !strings.Contains(strings.Join(candidates, "\n"), host) {
			t.Fatalf("候选源缺少 %s", host)
		}
	}
	if got := candidateURLs("https://repo.jellyfin.org/files/server.zip"); len(got) != 1 || got[0] != "https://repo.jellyfin.org/files/server.zip" {
		t.Fatalf("Jellyfin 应只使用官方源: %v", got)
	}
}

func TestComponentProbeReadsLockedMetadata(t *testing.T) {
	artifacts := []managed.Artifact{{Component: "filebrowser", Platform: "linux", Arch: "amd64", URL: "https://github.com/example/filebrowser", Size: 42}}
	probeURL, probeSize, err := componentProbe(artifacts, "filebrowser/linux/amd64")
	if err != nil || probeURL != artifacts[0].URL || probeSize != artifacts[0].Size {
		t.Fatalf("测速资产元数据错误: url=%s size=%d err=%v", probeURL, probeSize, err)
	}
	if _, _, err := componentProbe(artifacts, "filebrowser/linux/arm64"); err == nil {
		t.Fatal("缺失的测速资产未返回错误")
	}
}
