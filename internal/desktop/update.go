package desktop

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/releaseproxy"
)

type UpdateStatus struct {
	State          string    `json:"state"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version,omitempty"`
	PublishedAt    time.Time `json:"published_at,omitempty"`
	Notes          string    `json:"notes,omitempty"`
	Downloaded     int64     `json:"downloaded,omitempty"`
	Total          int64     `json:"total,omitempty"`
	Signature      string    `json:"signature"`
	Mode           string    `json:"mode"`
	Error          string    `json:"error,omitempty"`
	SkippedVersion string    `json:"skipped_version,omitempty"`
}

type UpdateService struct {
	mu             sync.RWMutex
	updater        *updater.Updater
	app            *application.App
	status         UpdateStatus
	pendingVersion string
}

type signedProvider struct {
	updater.Provider
}

func newUpdateService() *UpdateService {
	return &UpdateService{status: UpdateStatus{
		State: "idle", CurrentVersion: buildinfo.Version, Signature: "等待校验", Mode: desktopUpdateMode(),
	}}
}

func (s *UpdateService) Configure(app *application.App) error {
	manifest, err := url.Parse(buildinfo.UpdateManifestURL)
	if err != nil || !releaseproxy.IsProxyURL(manifest.String()) {
		return errors.New("更新清单必须使用固定公开加速地址")
	}
	publicKey, err := decodeUpdatePublicKey(buildinfo.UpdatePublicKey)
	if err != nil {
		return err
	}
	provider, err := endpoint.New(endpoint.Config{URL: buildinfo.UpdateManifestURL, HTTPClient: releaseproxy.NewClient(20 * time.Minute)})
	if err != nil {
		return fmt.Errorf("创建 Wails Endpoint Provider 失败: %w", err)
	}
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: strings.TrimPrefix(buildinfo.Version, "v"), Providers: []updater.Provider{signedProvider{Provider: provider}},
		PublicKey: publicKey, Window: updater.WindowNone,
	}); err != nil {
		return fmt.Errorf("初始化 Wails 更新器失败: %w", err)
	}
	s.updater = app.Updater
	s.app = app
	app.Event.On(updater.EventDownloadProgress, func(event *application.CustomEvent) {
		data, _ := json.Marshal(event.Data)
		var progress updater.Progress
		if json.Unmarshal(data, &progress) == nil {
			s.mu.Lock()
			s.status.Downloaded = progress.Written
			s.status.Total = progress.Total
			s.mu.Unlock()
		}
	})
	return nil
}

func (p signedProvider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	release, err := p.Provider.Check(ctx, request)
	if err != nil || release == nil {
		return release, err
	}
	verification := release.Verification
	if verification == nil || verification.SignatureAlgo != "ed25519" || len(verification.Signature) != ed25519.SignatureSize {
		return nil, errors.New("更新资产缺少有效 Ed25519 签名")
	}
	if verification.DigestAlgo != "sha256" || len(verification.Digest) != 32 {
		return nil, errors.New("更新资产缺少有效 SHA-256 摘要")
	}
	if release.Artifact.Platform != request.Platform || release.Artifact.Arch != request.Arch || release.Artifact.Filename == "" || release.Artifact.Size <= 0 {
		return nil, errors.New("更新资产的平台、架构、文件名或大小无效")
	}
	artifactURL, ok := release.Metadata["endpoint.artifact.url"].(string)
	if !ok || !releaseproxy.IsOfficialURL(artifactURL) {
		return nil, errors.New("更新资产不是批准的 HomeStack Release 地址")
	}
	return release, nil
}

func (s *UpdateService) Status() UpdateStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	if s.updater != nil {
		status.SkippedVersion = s.updater.SkippedVersion()
	}
	return status
}

func (s *UpdateService) Check() (UpdateStatus, error) {
	if s.updater == nil {
		return s.fail(errors.New("桌面更新器未配置"))
	}
	s.setState("checking", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	release, err := s.updater.Check(ctx)
	if err != nil {
		return s.fail(err)
	}
	s.mu.Lock()
	if release == nil {
		s.status.State = "up-to-date"
		s.status.Error = ""
		s.status.Downloaded = 0
		s.status.Total = 0
		s.pendingVersion = ""
	} else {
		s.status.State = "available"
		s.status.LatestVersion = release.Version
		s.status.PublishedAt = release.PublishedAt
		s.status.Notes = release.Notes
		s.status.Downloaded = 0
		s.status.Total = release.Artifact.Size
		s.status.Signature = "签名已声明，等待下载校验"
		s.status.Error = ""
		s.pendingVersion = release.Version
	}
	status := s.status
	s.mu.Unlock()
	return status, nil
}

func (s *UpdateService) DownloadAndInstall() (UpdateStatus, error) {
	if s.updater == nil {
		return s.fail(errors.New("桌面更新器未配置"))
	}
	if s.Status().Mode == "deb" {
		return s.fail(errors.New("deb 安装由系统包管理器负责，请从 GitHub Release 下载新版本"))
	}
	s.mu.Lock()
	s.status.State = "downloading"
	s.status.Downloaded = 0
	s.status.Error = ""
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := s.updater.DownloadAndInstall(ctx); err != nil {
		return s.fail(err)
	}
	s.setState("verifying", "")
	if err := validateStagedVersion(ctx, s.updater.DownloadedPath(), s.pendingVersion); err != nil {
		return s.fail(err)
	}
	s.mu.Lock()
	s.status.State = "ready"
	s.status.Signature = "Ed25519 与内嵌版本校验通过"
	s.status.Error = ""
	status := s.status
	s.mu.Unlock()
	return status, nil
}

func (s *UpdateService) Restart() error {
	if s.updater == nil {
		return errors.New("桌面更新器未配置")
	}
	if s.Status().State != "ready" {
		return errors.New("更新尚未通过校验，不能重启")
	}
	if s.Status().Mode == "appimage" {
		return restartAppImage(s.app, s.updater.DownloadedPath())
	}
	return s.updater.Restart(context.Background())
}

func (s *UpdateService) SkipVersion(version string) error {
	if s.updater == nil {
		return errors.New("桌面更新器未配置")
	}
	if version == "" || version != s.pendingVersion {
		return errors.New("只能忽略当前检测到的更新版本")
	}
	s.updater.SkipVersion(version)
	s.mu.Lock()
	s.pendingVersion = ""
	s.status.State = "up-to-date"
	s.status.Error = ""
	s.mu.Unlock()
	return nil
}

func (s *UpdateService) OpenReleasePage() error {
	return openSystemURL("https://github.com/shusfun/HomeStack/releases/latest")
}

func (s *UpdateService) setState(state, message string) {
	s.mu.Lock()
	s.status.State = state
	s.status.Error = message
	s.mu.Unlock()
}

func (s *UpdateService) fail(err error) (UpdateStatus, error) {
	s.setState("error", err.Error())
	return s.Status(), err
}

func decodeUpdatePublicKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("桌面更新 Ed25519 公钥未内置")
	}
	encodings := []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(strings.TrimSpace(raw))
		if err == nil && len(decoded) == ed25519.PublicKeySize {
			return decoded, nil
		}
	}
	return nil, errors.New("桌面更新 Ed25519 公钥编码无效")
}

func validateStagedVersion(ctx context.Context, stagedPath, expectedVersion string) error {
	if stagedPath == "" || expectedVersion == "" {
		return errors.New("暂存更新路径或目标版本为空")
	}
	executable := stagedPath
	info, err := os.Stat(stagedPath)
	if err != nil {
		return fmt.Errorf("读取暂存更新失败: %w", err)
	}
	if info.IsDir() {
		executable = filepath.Join(stagedPath, "Contents", "MacOS", "HomeStack")
	} else if runtime.GOOS == "linux" && info.Mode().Perm()&0o100 == 0 {
		if err := os.Chmod(executable, info.Mode().Perm()|0o100); err != nil {
			return fmt.Errorf("设置 Linux 暂存程序执行权限失败: %w", err)
		}
	}
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandContext, executable, "--version-json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("执行暂存程序版本校验失败: %s", strings.TrimSpace(string(output)))
	}
	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		GOOS    string `json:"goos"`
		GOARCH  string `json:"goarch"`
	}
	if err := json.Unmarshal(output, &version); err != nil {
		return fmt.Errorf("解析暂存程序版本失败: %w", err)
	}
	if version.Name != "homestack-desktop" || strings.TrimPrefix(version.Version, "v") != strings.TrimPrefix(expectedVersion, "v") || version.GOOS != runtime.GOOS || version.GOARCH != runtime.GOARCH {
		return fmt.Errorf("暂存程序版本、平台或架构不匹配: %s %s %s/%s", version.Name, version.Version, version.GOOS, version.GOARCH)
	}
	return nil
}

func desktopUpdateMode() string {
	if runtime.GOOS != "linux" {
		return "self-update"
	}
	if os.Getenv("APPIMAGE") != "" {
		return "appimage"
	}
	executable, err := os.Executable()
	if err == nil && strings.HasPrefix(filepath.Clean(executable), "/usr/bin/") {
		return "deb"
	}
	return "portable"
}
