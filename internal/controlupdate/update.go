package controlupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/releaseproxy"
	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

type Status struct {
	State          string    `json:"state"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version,omitempty"`
	PublishedAt    time.Time `json:"published_at,omitempty"`
	Notes          string    `json:"notes,omitempty"`
	Downloaded     int64     `json:"downloaded,omitempty"`
	Total          int64     `json:"total,omitempty"`
	Signature      string    `json:"signature"`
	Error          string    `json:"error,omitempty"`
}

type artifact struct {
	Component     string `json:"component"`
	URL           string `json:"url"`
	Filename      string `json:"filename"`
	Filetype      string `json:"filetype"`
	Size          int64  `json:"size"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	DigestAlgo    string `json:"digestAlgo"`
	Digest        string `json:"digest"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     string `json:"signature"`
}

type manifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	Version       string     `json:"version"`
	Channel       string     `json:"channel"`
	Name          string     `json:"name"`
	PublishedAt   string     `json:"publishedAt"`
	Notes         string     `json:"notes"`
	Artifacts     []artifact `json:"artifacts"`
}

type Options struct {
	CurrentVersion string
	ManifestURL    string
	PublicKey      string
	StateDir       string
	Platform       string
	Architecture   string
	HTTPClient     *http.Client
	Installer      setupapi.ControlUpdateInstaller
}

type Updater struct {
	operationMu  sync.Mutex
	mu           sync.RWMutex
	status       Status
	manifestURL  string
	publicKey    ed25519.PublicKey
	stateDir     string
	platform     string
	architecture string
	client       *http.Client
	installer    setupapi.ControlUpdateInstaller
	pending      *artifact
	archivePath  string
}

func New(options Options) (*Updater, error) {
	if !releaseproxy.IsProxyURL(options.ManifestURL) {
		return nil, errors.New("Control 更新清单必须使用固定公开加速地址")
	}
	publicKey, err := decodeBase64(options.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("Control 更新 Ed25519 公钥无效")
	}
	if options.CurrentVersion == "" || options.StateDir == "" || !filepath.IsAbs(options.StateDir) || options.Installer == nil {
		return nil, errors.New("Control 更新器配置不完整")
	}
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	if options.Architecture == "" {
		options.Architecture = runtime.GOARCH
	}
	if options.Platform != "linux" || options.Architecture != "amd64" && options.Architecture != "arm64" {
		return nil, errors.New("Control 更新只支持 Linux amd64/arm64")
	}
	if options.HTTPClient == nil {
		options.HTTPClient = releaseproxy.NewClient(20 * time.Minute)
	}
	return &Updater{
		status:      Status{State: "idle", CurrentVersion: strings.TrimPrefix(options.CurrentVersion, "v"), Signature: "等待校验"},
		manifestURL: options.ManifestURL, publicKey: ed25519.PublicKey(publicKey), stateDir: options.StateDir,
		platform: options.Platform, architecture: options.Architecture, client: options.HTTPClient, installer: options.Installer,
	}, nil
}

func (u *Updater) Status() Status {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.status
}

func (u *Updater) Check(ctx context.Context) (Status, error) {
	u.operationMu.Lock()
	defer u.operationMu.Unlock()
	u.setState("checking", "")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.manifestURL, nil)
	if err != nil {
		return u.fail(err)
	}
	response, err := u.client.Do(request)
	if err != nil {
		return u.fail(fmt.Errorf("下载 Control 更新清单失败: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return u.fail(fmt.Errorf("下载 Control 更新清单失败: HTTP %d", response.StatusCode))
	}
	var value manifest
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return u.fail(fmt.Errorf("解析 Control 更新清单失败: %w", err))
	}
	if value.SchemaVersion != 1 {
		return u.fail(fmt.Errorf("Control 更新清单 schemaVersion 必须为 1，实际为 %d", value.SchemaVersion))
	}
	newer, err := semverNewer(value.Version, u.status.CurrentVersion)
	if err != nil {
		return u.fail(err)
	}
	if !newer {
		u.mu.Lock()
		u.pending, u.archivePath = nil, ""
		u.status.State, u.status.LatestVersion, u.status.Error = "up-to-date", strings.TrimPrefix(value.Version, "v"), ""
		status := u.status
		u.mu.Unlock()
		return status, nil
	}
	var selected *artifact
	for index := range value.Artifacts {
		candidate := &value.Artifacts[index]
		if candidate.Component == "control" && candidate.Platform == u.platform && candidate.Arch == u.architecture {
			if selected != nil {
				return u.fail(errors.New("Control 更新清单包含重复的平台资产"))
			}
			selected = candidate
		}
	}
	if selected == nil {
		return u.fail(fmt.Errorf("Control 更新清单缺少 %s/%s 资产", u.platform, u.architecture))
	}
	version := strings.TrimPrefix(value.Version, "v")
	if err := validateArtifact(*selected, version, u.architecture); err != nil {
		return u.fail(err)
	}
	publishedAt, err := time.Parse(time.RFC3339, value.PublishedAt)
	if err != nil {
		return u.fail(fmt.Errorf("Control 更新发布时间无效: %w", err))
	}
	u.mu.Lock()
	copy := *selected
	u.pending = &copy
	u.archivePath = ""
	u.status.State, u.status.LatestVersion, u.status.PublishedAt = "available", version, publishedAt
	u.status.Notes, u.status.Downloaded, u.status.Total = value.Notes, 0, selected.Size
	u.status.Signature, u.status.Error = "签名已声明，等待下载校验", ""
	status := u.status
	u.mu.Unlock()
	return status, nil
}

func (u *Updater) Download(ctx context.Context) (Status, error) {
	u.operationMu.Lock()
	defer u.operationMu.Unlock()
	u.mu.RLock()
	if u.pending == nil {
		u.mu.RUnlock()
		return u.fail(errors.New("尚未检查到 Control 更新"))
	}
	current, version := *u.pending, u.status.LatestVersion
	u.mu.RUnlock()
	u.setState("downloading", "")
	directory := filepath.Join(u.stateDir, "updates", version)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return u.fail(fmt.Errorf("创建 Control 更新暂存目录失败: %w", err))
	}
	destination := filepath.Join(directory, current.Filename)
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return u.fail(fmt.Errorf("清理旧 Control 更新暂存文件失败: %w", err))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, current.URL, nil)
	if err != nil {
		return u.fail(err)
	}
	response, err := u.client.Do(request)
	if err != nil {
		return u.fail(fmt.Errorf("下载 Control 更新失败: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return u.fail(fmt.Errorf("下载 Control 更新失败: HTTP %d", response.StatusCode))
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return u.fail(fmt.Errorf("创建 Control 更新暂存文件失败: %w", err))
	}
	written, copyErr := io.Copy(writerFunc{write: file.Write, progress: func(count int64) {
		u.mu.Lock()
		u.status.Downloaded += count
		u.mu.Unlock()
	}}, io.LimitReader(response.Body, current.Size+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != current.Size {
		_ = os.Remove(destination)
		return u.fail(fmt.Errorf("Control 更新下载大小不匹配: 预期 %d，实际 %d", current.Size, written))
	}
	u.setState("verifying", "")
	if err := verifyArtifact(destination, current, u.publicKey); err != nil {
		_ = os.Remove(destination)
		return u.fail(err)
	}
	u.mu.Lock()
	u.archivePath = destination
	u.status.State, u.status.Signature, u.status.Error = "ready", "Ed25519 与 SHA-256 校验通过", ""
	status := u.status
	u.mu.Unlock()
	return status, nil
}

func (u *Updater) Install(ctx context.Context) (Status, error) {
	u.operationMu.Lock()
	defer u.operationMu.Unlock()
	u.mu.RLock()
	if u.pending == nil || u.archivePath == "" {
		u.mu.RUnlock()
		return u.fail(errors.New("Control 更新尚未完成下载校验"))
	}
	current, version, archivePath := *u.pending, u.status.LatestVersion, u.archivePath
	u.mu.RUnlock()
	request := setupapi.ControlUpdateInstallation{Version: version, ArchivePath: archivePath, Filename: current.Filename, Size: current.Size, Digest: current.Digest, Signature: current.Signature}
	if err := u.installer.InstallControlUpdate(ctx, request); err != nil {
		return u.fail(fmt.Errorf("安装 Control 更新失败: %w", err))
	}
	u.setState("installing", "")
	return u.Status(), nil
}

type writerFunc struct {
	write    func([]byte) (int, error)
	progress func(int64)
}

func (w writerFunc) Write(data []byte) (int, error) {
	count, err := w.write(data)
	w.progress(int64(count))
	return count, err
}

func validateArtifact(value artifact, version, architecture string) error {
	expected := "homestack-control-update_" + version + "_linux_" + architecture + ".tar.gz"
	if value.Filename != expected || !releaseproxy.IsOfficialURL(value.URL) || !strings.HasSuffix(value.URL, "/"+expected) {
		return errors.New("Control 更新资产名称或地址无效")
	}
	if value.Size <= 0 || value.Size > 512<<20 || value.DigestAlgo != "sha256" || value.SignatureAlgo != "ed25519" || value.Digest == "" || value.Signature == "" {
		return errors.New("Control 更新资产大小、摘要或签名字段无效")
	}
	return nil
}

func verifyArtifact(path string, value artifact, publicKey ed25519.PublicKey) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取 Control 更新资产失败: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(file, value.Size+1))
	if err != nil || written != value.Size {
		return errors.New("Control 更新资产大小校验失败")
	}
	expected, err := decodeDigest(value.Digest)
	actual := digest.Sum(nil)
	if err != nil || len(expected) != sha256.Size || !strings.EqualFold(hex.EncodeToString(actual), hex.EncodeToString(expected)) {
		return errors.New("Control 更新 SHA-256 校验失败")
	}
	signature, err := decodeBase64(value.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, actual, signature) {
		return errors.New("Control 更新 Ed25519 签名校验失败")
	}
	return nil
}

func decodeDigest(raw string) ([]byte, error) {
	if decoded, err := hex.DecodeString(raw); err == nil {
		return decoded, nil
	}
	return decodeBase64(raw)
}

func decodeBase64(raw string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if decoded, err := encoding.DecodeString(strings.TrimSpace(raw)); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("base64 编码无效")
}

func semverNewer(candidate, current string) (bool, error) {
	parse := func(raw string) ([3]int, string, error) {
		var core [3]int
		parts := strings.SplitN(strings.TrimPrefix(raw, "v"), "-", 2)
		numbers := strings.Split(parts[0], ".")
		if len(numbers) != 3 {
			return core, "", errors.New("版本必须使用 major.minor.patch 格式")
		}
		for index, value := range numbers {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				return core, "", errors.New("版本号包含无效数值")
			}
			core[index] = parsed
		}
		pre := ""
		if len(parts) == 2 {
			pre = parts[1]
		}
		return core, pre, nil
	}
	want, wantPre, err := parse(candidate)
	if err != nil {
		return false, fmt.Errorf("更新版本无效: %w", err)
	}
	have, havePre, err := parse(current)
	if err != nil {
		return false, fmt.Errorf("当前版本无效: %w", err)
	}
	for index := range want {
		if want[index] != have[index] {
			return want[index] > have[index], nil
		}
	}
	if wantPre == havePre {
		return false, nil
	}
	if wantPre == "" {
		return true, nil
	}
	if havePre == "" {
		return false, nil
	}
	return wantPre > havePre, nil
}

func (u *Updater) setState(state, message string) {
	u.mu.Lock()
	u.status.State, u.status.Error = state, message
	u.mu.Unlock()
}

func (u *Updater) fail(err error) (Status, error) {
	u.setState("error", err.Error())
	return u.Status(), err
}
