package managed

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	FileBrowserVersion            = "0.3.5"
	JellyfinVersion               = "10.11.11"
	FFmpegVersion                 = "7.1.4-3"
	managedContentDownloadTimeout = 60 * time.Minute
)

func Prepare(ctx context.Context, stateDir, manifestURL string, publicKey ed25519.PublicKey, existing *Profile) (Profile, error) {
	if existing != nil {
		if err := ValidateProfile(*existing); err == nil {
			return *existing, nil
		}
	}
	if !filepath.IsAbs(stateDir) {
		return Profile{}, errors.New("托管组件状态目录必须是绝对路径")
	}
	client := &http.Client{Timeout: managedContentDownloadTimeout}
	manifest, err := FetchManifest(ctx, client, manifestURL, publicKey)
	if err != nil {
		return Profile{}, err
	}
	fileArtifact, err := manifest.Current("filebrowser")
	if err != nil {
		return Profile{}, err
	}
	mediaArtifact, err := manifest.Current("jellyfin")
	if err != nil {
		return Profile{}, err
	}
	ffmpegArtifact, err := manifest.Current("jellyfin-ffmpeg")
	if err != nil {
		return Profile{}, err
	}
	if fileArtifact.Version != FileBrowserVersion || mediaArtifact.Version != JellyfinVersion || ffmpegArtifact.Version != FFmpegVersion {
		return Profile{}, fmt.Errorf("组件清单版本与应用契约不一致: filebrowser=%s jellyfin=%s ffmpeg=%s", fileArtifact.Version, mediaArtifact.Version, ffmpegArtifact.Version)
	}
	installer := Installer{Client: client, Root: filepath.Join(stateDir, "components")}
	fileBrowser, err := installer.Ensure(ctx, fileArtifact)
	if err != nil {
		return Profile{}, err
	}
	jellyfin, err := installer.Ensure(ctx, mediaArtifact)
	if err != nil {
		return Profile{}, err
	}
	ffmpeg, err := installer.Ensure(ctx, ffmpegArtifact)
	if err != nil {
		return Profile{}, err
	}
	mediaPassword, err := randomSecret()
	if err != nil {
		return Profile{}, err
	}
	managedState := filepath.Join(stateDir, "managed")
	profile := Profile{SchemaVersion: ProfileSchema, StateDir: managedState, FileRoot: filepath.Join(managedState, "filebrowser", "root"), FileBrowser: fileBrowser, Jellyfin: jellyfin, FFmpeg: ffmpeg, JellyfinPassword: mediaPassword, ModuleSecrets: map[string]map[string]string{}}
	if err := ValidateProfile(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func ValidateProfile(profile Profile) error {
	if profile.SchemaVersion != ProfileSchema {
		return fmt.Errorf("托管内容档案 schema 不受支持: %d", profile.SchemaVersion)
	}
	if !filepath.IsAbs(profile.StateDir) || !filepath.IsAbs(profile.FileRoot) || profile.FileBrowser.Component != "filebrowser" || profile.FileBrowser.Version != FileBrowserVersion || profile.Jellyfin.Component != "jellyfin" || profile.Jellyfin.Version != JellyfinVersion || profile.FFmpeg.Component != "jellyfin-ffmpeg" || profile.FFmpeg.Version != FFmpegVersion || profile.JellyfinPassword == "" {
		return errors.New("托管内容档案不完整")
	}
	for _, installation := range []Installation{profile.FileBrowser, profile.Jellyfin, profile.FFmpeg} {
		digest, err := hex.DecodeString(installation.ArtifactSHA256)
		if err != nil || len(digest) != sha256.Size {
			return errors.New("托管组件档案缺少有效的资产摘要")
		}
	}
	for _, path := range []string{profile.FileBrowser.Executable, profile.Jellyfin.Executable, profile.Jellyfin.WebDir, profile.FFmpeg.Executable} {
		if !filepath.IsAbs(path) {
			return errors.New("托管内容档案包含非绝对路径")
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("托管内容文件不可用: %w", err)
		}
		if path == profile.Jellyfin.WebDir {
			if !info.IsDir() {
				return errors.New("Jellyfin Web 路径不是目录")
			}
		} else if !info.Mode().IsRegular() {
			return errors.New("托管组件可执行文件不是常规文件")
		}
	}
	return nil
}

func randomSecret() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
