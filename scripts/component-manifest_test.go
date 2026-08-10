//go:build ignore

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wangshangbin/homestack/internal/managed"
)

func TestComponentSourcesCoverDesktopPlatforms(t *testing.T) {
	sources := componentSources()
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
}

func TestDownloadRemoteWritesVerifiedMirror(t *testing.T) {
	payload := []byte("component")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "component.bin")
	artifact := managed.Artifact{Component: "filebrowser", Platform: "linux", Arch: "amd64", URL: server.URL}
	size, digest, err := downloadRemote(context.Background(), server.Client(), artifact, target, "")
	if err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(payload))
	if size != int64(len(payload)) || digest != expected {
		t.Fatalf("镜像资产元数据错误: size=%d digest=%s", size, digest)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("镜像资产内容错误: %q", data)
	}
}

func TestDownloadRemoteRequiresUPXForDarwinFileBrowser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("packed"))
	}))
	defer server.Close()
	artifact := managed.Artifact{Component: "filebrowser", Platform: "darwin", Arch: "amd64", URL: server.URL}
	_, _, err := downloadRemote(context.Background(), server.Client(), artifact, filepath.Join(t.TempDir(), "filebrowser"), "")
	if err == nil || !strings.Contains(err.Error(), "固定 UPX") {
		t.Fatalf("macOS FileBrowser 缺少 UPX 时未返回真实错误: %v", err)
	}
}

func TestMirrorReleaseURL(t *testing.T) {
	artifact := managed.Artifact{Component: "jellyfin", Version: "10.11.11", Platform: "darwin", Arch: "amd64", Format: "tar.xz"}
	filename := mirrorFilename(artifact)
	if filename != "jellyfin_10.11.11_darwin_amd64.tar.xz" {
		t.Fatalf("镜像文件名错误: %s", filename)
	}
	if got := releaseAssetURL("shusfun/HomeStack", "v0.2.9", filename); got != "https://github.com/shusfun/HomeStack/releases/download/v0.2.9/jellyfin_10.11.11_darwin_amd64.tar.xz" {
		t.Fatalf("镜像 URL 错误: %s", got)
	}
}
