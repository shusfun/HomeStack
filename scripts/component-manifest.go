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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/managed"
)

func main() {
	output := flag.String("output", "dist/components.json", "组件清单输出路径")
	privateEncoded := flag.String("private-key", "", "base64 Ed25519 私钥")
	flag.Parse()
	privateKey := decodeKey(*privateEncoded)
	if len(privateKey) != ed25519.PrivateKeySize {
		fatal(errors.New("组件清单需要有效的 base64 Ed25519 私钥"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Minute}
	artifacts := componentSources()
	cache := make(map[string]struct {
		size   int64
		digest string
	})
	for index := range artifacts {
		current, ok := cache[artifacts[index].URL]
		if !ok {
			current.size, current.digest, err = inspectRemote(ctx, client, artifacts[index])
			if err != nil {
				fatal(err)
			}
			cache[artifacts[index].URL] = current
		}
		artifacts[index].Size = current.size
		artifacts[index].SHA256 = current.digest
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

func inspectRemote(ctx context.Context, client *http.Client, artifact managed.Artifact) (int64, string, error) {
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
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(response.Body, 512<<20+1))
	if err != nil {
		return 0, "", err
	}
	if size < 1 || size > 512<<20 {
		return 0, "", fmt.Errorf("%s 官方资产大小超出限制: %d", artifact.Component, size)
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func componentSources() []managed.Artifact {
	const fileVersion = "0.3.5"
	const mediaVersion = "10.11.11"
	return []managed.Artifact{
		{Component: "filebrowser", Version: fileVersion, Platform: "darwin", Arch: "amd64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/darwin-amd64-filebrowser", Filename: "filebrowser", Format: "binary"},
		{Component: "filebrowser", Version: fileVersion, Platform: "darwin", Arch: "arm64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/darwin-arm64-filebrowser", Filename: "filebrowser", Format: "binary"},
		{Component: "filebrowser", Version: fileVersion, Platform: "windows", Arch: "amd64", URL: "https://github.com/gtsteffaniak/filebrowser/releases/download/v0.3.5/filebrowser.exe", Filename: "filebrowser.exe", Format: "binary"},
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
