//go:build ignore

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/managed"
)

func main() {
	output := flag.String("output", "dist/components.json", "组件清单输出路径")
	dist := flag.String("dist", "dist", "组件镜像资产输出目录")
	repository := flag.String("repository", "", "GitHub 仓库，例如 owner/repo")
	tag := flag.String("tag", "", "GitHub Release 标签")
	upx := flag.String("upx", "", "用于解压 macOS FileBrowser 的固定 UPX 绝对路径")
	privateEncoded := flag.String("private-key", "", "base64 Ed25519 私钥")
	flag.Parse()
	privateKey := decodeKey(*privateEncoded)
	if len(privateKey) != ed25519.PrivateKeySize {
		fatal(errors.New("组件清单需要有效的 base64 Ed25519 私钥"))
	}
	if !validRepository.MatchString(*repository) || !validTag.MatchString(*tag) {
		fatal(errors.New("组件镜像需要有效的 GitHub 仓库和 Release 标签"))
	}
	if err := os.MkdirAll(*dist, 0o755); err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Minute}
	artifacts := componentSources()
	cache := make(map[string]struct {
		size   int64
		digest string
		url    string
	})
	for index := range artifacts {
		current, ok := cache[artifacts[index].URL]
		if !ok {
			filename := mirrorFilename(artifacts[index])
			var err error
			current.size, current.digest, err = downloadRemote(ctx, client, artifacts[index], filepath.Join(*dist, filename), *upx)
			if err != nil {
				fatal(err)
			}
			current.url = releaseAssetURL(*repository, *tag, filename)
			cache[artifacts[index].URL] = current
		}
		artifacts[index].Size = current.size
		artifacts[index].SHA256 = current.digest
		artifacts[index].URL = current.url
	}
	data, err := managed.SignManifest(managed.Manifest{SchemaVersion: managed.ManifestSchema, Artifacts: artifacts}, ed25519.PrivateKey(privateKey))
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fatal(err)
	}
}

func decodeKey(raw string) []byte {
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		decoded, err := encoding.DecodeString(strings.TrimSpace(raw))
		if err == nil && len(decoded) == ed25519.PrivateKeySize {
			return decoded
		}
	}
	return nil
}

var (
	validRepository = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	validTag        = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[.-][A-Za-z0-9.-]+)?$`)
)

func downloadRemote(ctx context.Context, client *http.Client, artifact managed.Artifact, target, upxBinary string) (int64, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return 0, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("读取 %s %s/%s 官方资产失败: %w", artifact.Component, artifact.Platform, artifact.Arch, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("读取 %s 官方资产失败: HTTP %d", artifact.Component, response.StatusCode)
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".homestack-component-*")
	if err != nil {
		return 0, "", err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	size, copyErr := io.Copy(file, io.LimitReader(response.Body, 512<<20+1))
	closeErr := file.Close()
	if copyErr != nil {
		return 0, "", copyErr
	}
	if closeErr != nil {
		return 0, "", closeErr
	}
	if size < 1 || size > 512<<20 {
		return 0, "", fmt.Errorf("%s 官方资产大小超出限制: %d", artifact.Component, size)
	}
	if requiresUPXUnpack(artifact) {
		if err := unpackUPX(ctx, upxBinary, temporary); err != nil {
			return 0, "", err
		}
	}
	size, digest, err := inspectLocalAsset(temporary)
	if err != nil {
		return 0, "", err
	}
	if err := os.Chmod(temporary, 0o644); err != nil {
		return 0, "", err
	}
	if err := os.Rename(temporary, target); err != nil {
		return 0, "", fmt.Errorf("保存 %s 镜像资产失败: %w", artifact.Component, err)
	}
	return size, digest, nil
}

func requiresUPXUnpack(artifact managed.Artifact) bool {
	return artifact.Component == "filebrowser" && artifact.Platform == "darwin"
}

func unpackUPX(ctx context.Context, binary, target string) error {
	if !filepath.IsAbs(binary) {
		return errors.New("解压 macOS FileBrowser 需要固定 UPX 绝对路径")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("固定 UPX 不存在或不可执行")
	}
	output, err := exec.CommandContext(ctx, binary, "-d", target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("解压 macOS FileBrowser 失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func inspectLocalAsset(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, 512<<20+1))
	if err != nil {
		return 0, "", err
	}
	if size < 1 || size > 512<<20 {
		return 0, "", fmt.Errorf("镜像资产大小超出限制: %d", size)
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func mirrorFilename(artifact managed.Artifact) string {
	extension := map[string]string{"binary": "", "zip": ".zip", "tar.gz": ".tar.gz", "tar.xz": ".tar.xz"}[artifact.Format]
	if artifact.Format == "binary" && artifact.Platform == "windows" {
		extension = ".exe"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", artifact.Component, artifact.Version, artifact.Platform, artifact.Arch, extension)
}

func releaseAssetURL(repository, tag, filename string) string {
	return "https://github.com/" + repository + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(filename)
}

func componentSources() []managed.Artifact {
	const fileVersion = "0.3.5"
	const mediaVersion = "10.11.11"
	return []managed.Artifact{
		{Component: "filebrowser", Version: fileVersion, Platform: "darwin", Arch: "amd64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/darwin-amd64-filebrowser", Filename: "filebrowser", Format: "binary"},
		{Component: "filebrowser", Version: fileVersion, Platform: "darwin", Arch: "arm64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/darwin-arm64-filebrowser", Filename: "filebrowser", Format: "binary"},
		{Component: "filebrowser", Version: fileVersion, Platform: "windows", Arch: "amd64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/filebrowser.exe", Filename: "filebrowser.exe", Format: "binary"},
		{Component: "filebrowser", Version: fileVersion, Platform: "windows", Arch: "arm64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/filebrowser.exe", Filename: "filebrowser.exe", Format: "binary"},
		{Component: "filebrowser", Version: fileVersion, Platform: "linux", Arch: "amd64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/linux-amd64-filebrowser", Filename: "filebrowser", Format: "binary"},
		{Component: "filebrowser", Version: fileVersion, Platform: "linux", Arch: "arm64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/linux-arm64-filebrowser", Filename: "filebrowser", Format: "binary"},
		{Component: "jellyfin", Version: mediaVersion, Platform: "darwin", Arch: "amd64", URL: "https://repo.jellyfin.org/files/server/macos/stable/v10.11.11/amd64/jellyfin_10.11.11-amd64.tar.xz", Filename: "jellyfin.tar.xz", Format: "tar.xz"},
		{Component: "jellyfin", Version: mediaVersion, Platform: "darwin", Arch: "arm64", URL: "https://repo.jellyfin.org/files/server/macos/stable/v10.11.11/arm64/jellyfin_10.11.11-arm64.tar.xz", Filename: "jellyfin.tar.xz", Format: "tar.xz"},
		{Component: "jellyfin", Version: mediaVersion, Platform: "windows", Arch: "amd64", URL: "https://repo.jellyfin.org/files/server/windows/stable/v10.11.11/amd64/jellyfin_10.11.11-amd64.zip", Filename: "jellyfin.zip", Format: "zip"},
		{Component: "jellyfin", Version: mediaVersion, Platform: "windows", Arch: "arm64", URL: "https://repo.jellyfin.org/files/server/windows/stable/v10.11.11/arm64/jellyfin_10.11.11-arm64.zip", Filename: "jellyfin.zip", Format: "zip"},
		{Component: "jellyfin", Version: mediaVersion, Platform: "linux", Arch: "amd64", URL: "https://repo.jellyfin.org/files/server/linux/stable/v10.11.11/amd64/jellyfin_10.11.11-amd64.tar.gz", Filename: "jellyfin.tar.gz", Format: "tar.gz"},
		{Component: "jellyfin", Version: mediaVersion, Platform: "linux", Arch: "arm64", URL: "https://repo.jellyfin.org/files/server/linux/stable/v10.11.11/arm64/jellyfin_10.11.11-arm64.tar.gz", Filename: "jellyfin.tar.gz", Format: "tar.gz"},
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
