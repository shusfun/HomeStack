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
	"strings"
	"time"
)

const (
	FileBrowserVersion            = "1.5.1-stable"
	JellyfinVersion               = "10.11.11"
	FFmpegVersion                 = "7.1.4-3"
	managedContentDownloadTimeout = 60 * time.Minute
)

func Prepare(ctx context.Context, stateDir, manifestURL string, publicKey ed25519.PublicKey, existing *Profile) (Profile, error) {
	return PrepareWithProgress(ctx, stateDir, manifestURL, publicKey, existing, nil)
}

func PrepareWithProgress(ctx context.Context, stateDir, manifestURL string, publicKey ed25519.PublicKey, existing *Profile, report ProgressFunc) (Profile, error) {
	if existing != nil {
		if err := ValidateProfile(*existing); err == nil {
			for _, installation := range []Installation{existing.FileBrowser, existing.Jellyfin, existing.FFmpeg} {
				if report != nil {
					report(Progress{Component: installation.Component, Version: installation.Version, Phase: PhaseReady, SourceHost: installation.SourceHost})
				}
			}
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
	if report != nil {
		for _, artifact := range []Artifact{fileArtifact, mediaArtifact, ffmpegArtifact} {
			report(Progress{Component: artifact.Component, Version: artifact.Version, Phase: PhasePending, Total: artifact.Size})
		}
	}
	managedState := filepath.Join(stateDir, "managed")
	mediaPassword, moduleSecrets := existingManagedCredentials(existing, managedState)
	installer := Installer{Client: client, Root: filepath.Join(stateDir, "components"), Progress: report}
	fileBrowser, err := ensureCurrentInstallation(ctx, installer, fileArtifact, existingInstallation(existing, "filebrowser"))
	if err != nil {
		return Profile{}, err
	}
	jellyfin, err := ensureCurrentInstallation(ctx, installer, mediaArtifact, existingInstallation(existing, "jellyfin"))
	if err != nil {
		return Profile{}, err
	}
	ffmpeg, err := ensureCurrentInstallation(ctx, installer, ffmpegArtifact, existingInstallation(existing, "jellyfin-ffmpeg"))
	if err != nil {
		return Profile{}, err
	}
	if mediaPassword == "" {
		mediaPassword, err = randomSecret()
		if err != nil {
			return Profile{}, err
		}
	}
	profile := Profile{SchemaVersion: ProfileSchema, StateDir: managedState, FileRoot: filepath.Join(managedState, "filebrowser", "root"), FileBrowser: fileBrowser, Jellyfin: jellyfin, FFmpeg: ffmpeg, JellyfinPassword: mediaPassword, ModuleSecrets: moduleSecrets}
	if err := ValidateProfile(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func existingInstallation(profile *Profile, component string) Installation {
	if profile == nil {
		return Installation{}
	}
	switch component {
	case "filebrowser":
		return profile.FileBrowser
	case "jellyfin":
		return profile.Jellyfin
	case "jellyfin-ffmpeg":
		return profile.FFmpeg
	default:
		return Installation{}
	}
}

func ensureCurrentInstallation(ctx context.Context, installer Installer, artifact Artifact, existing Installation) (Installation, error) {
	if validateInstallation(existing, artifact.Component, artifact.Version) == nil && strings.EqualFold(existing.ArtifactSHA256, artifact.SHA256) {
		installer.report(artifact, PhaseReady, artifact.Size, artifact.Size, 0, existing.SourceHost, "")
		return existing, nil
	}
	return installer.Ensure(ctx, artifact)
}

func existingManagedCredentials(existing *Profile, managedState string) (string, map[string]map[string]string) {
	secrets := make(map[string]map[string]string)
	if existing == nil || existing.SchemaVersion != ProfileSchema || filepath.Clean(existing.StateDir) != filepath.Clean(managedState) || existing.JellyfinPassword == "" {
		return "", secrets
	}
	for module, values := range existing.ModuleSecrets {
		secrets[module] = make(map[string]string, len(values))
		for name, value := range values {
			secrets[module][name] = value
		}
	}
	return existing.JellyfinPassword, secrets
}

func ValidateProfile(profile Profile) error {
	if profile.SchemaVersion != ProfileSchema {
		return fmt.Errorf("托管内容档案 schema 不受支持: %d", profile.SchemaVersion)
	}
	if !filepath.IsAbs(profile.StateDir) || !filepath.IsAbs(profile.FileRoot) || profile.JellyfinPassword == "" {
		return errors.New("托管内容档案不完整")
	}
	for _, expected := range []struct {
		installation Installation
		component    string
		version      string
	}{{profile.FileBrowser, "filebrowser", FileBrowserVersion}, {profile.Jellyfin, "jellyfin", JellyfinVersion}, {profile.FFmpeg, "jellyfin-ffmpeg", FFmpegVersion}} {
		if err := validateInstallation(expected.installation, expected.component, expected.version); err != nil {
			return err
		}
	}
	return nil
}

func validateInstallation(installation Installation, component, version string) error {
	if installation.Component != component || installation.Version != version {
		return errors.New("托管内容档案不完整")
	}
	digest, err := hex.DecodeString(installation.ArtifactSHA256)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("托管组件档案缺少有效的资产摘要")
	}
	paths := []string{installation.Executable}
	if component == "jellyfin" {
		paths = append(paths, installation.WebDir)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return errors.New("托管内容档案包含非绝对路径")
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("托管内容文件不可用: %w", err)
		}
		if component == "jellyfin" && path == installation.WebDir {
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
