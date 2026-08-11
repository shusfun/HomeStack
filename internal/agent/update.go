package agent

import (
	"archive/tar"
	"compress/gzip"
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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/releaseproxy"
)

type AgentUpdateStatus struct {
	State          string     `json:"state"`
	CurrentVersion string     `json:"current_version"`
	LatestVersion  string     `json:"latest_version,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	Notes          string     `json:"notes,omitempty"`
	Downloaded     int64      `json:"downloaded,omitempty"`
	Total          int64      `json:"total,omitempty"`
	Signature      string     `json:"signature"`
	Error          string     `json:"error,omitempty"`
}

type updateArtifact struct {
	Component     string `json:"component"`
	URL           string `json:"url"`
	Filename      string `json:"filename"`
	Size          int64  `json:"size"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	DigestAlgo    string `json:"digestAlgo"`
	Digest        string `json:"digest"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     string `json:"signature"`
}

type updateManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Version       string           `json:"version"`
	PublishedAt   string           `json:"publishedAt"`
	Notes         string           `json:"notes"`
	Artifacts     []updateArtifact `json:"artifacts"`
}

type AgentUpdater struct {
	mu          sync.RWMutex
	manifestURL string
	publicKey   ed25519.PublicKey
	agentURL    string
	status      AgentUpdateStatus
	pending     *updateArtifact
	stagedPath  string
}

type Updater interface {
	Status() AgentUpdateStatus
	Check(context.Context) (AgentUpdateStatus, error)
	Download(context.Context) (AgentUpdateStatus, error)
	Install() error
}

type DisabledUpdater struct{}

func (DisabledUpdater) Status() AgentUpdateStatus {
	return AgentUpdateStatus{State: "disabled", Error: "桌面 Node 由 HomeStack App 统一更新"}
}
func (DisabledUpdater) Check(context.Context) (AgentUpdateStatus, error) {
	return AgentUpdateStatus{}, errors.New("桌面 Node 由 HomeStack App 统一更新")
}
func (DisabledUpdater) Download(context.Context) (AgentUpdateStatus, error) {
	return AgentUpdateStatus{}, errors.New("桌面 Node 由 HomeStack App 统一更新")
}
func (DisabledUpdater) Install() error {
	return errors.New("桌面 Node 由 HomeStack App 统一更新")
}

type updateProgressWriter struct {
	destination io.Writer
	written     int64
	onProgress  func(int64)
}

func (w *updateProgressWriter) Write(data []byte) (int, error) {
	count, err := w.destination.Write(data)
	w.written += int64(count)
	w.onProgress(w.written)
	return count, err
}

func NewAgentUpdater(manifestURL, publicKeyEncoded, agentURL string) (*AgentUpdater, error) {
	parsed, err := url.Parse(manifestURL)
	if err != nil || !releaseproxy.IsProxyURL(parsed.String()) {
		return nil, errors.New("Agent 更新清单必须使用固定公开加速地址")
	}
	publicKey, err := decodeAgentUpdatePublicKey(publicKeyEncoded)
	if err != nil {
		return nil, err
	}
	agent, err := url.Parse(agentURL)
	if err != nil || agent.Scheme != "https" || agent.Hostname() == "" {
		return nil, errors.New("Agent 健康检查地址无效")
	}
	return &AgentUpdater{
		manifestURL: manifestURL, publicKey: publicKey, agentURL: strings.TrimRight(agentURL, "/"),
		status: AgentUpdateStatus{State: "idle", CurrentVersion: buildinfo.Version, Signature: "等待校验"},
	}, nil
}

func (u *AgentUpdater) Status() AgentUpdateStatus {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.status
}

func (u *AgentUpdater) Check(ctx context.Context) (AgentUpdateStatus, error) {
	u.setState("checking", "")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.manifestURL, nil)
	if err != nil {
		return u.fail(err)
	}
	response, err := releaseproxy.NewClient(30 * time.Second).Do(request)
	if err != nil {
		return u.fail(fmt.Errorf("下载 Agent 更新清单失败: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return u.fail(fmt.Errorf("下载 Agent 更新清单失败: HTTP %d", response.StatusCode))
	}
	var manifest updateManifest
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&manifest); err != nil {
		return u.fail(fmt.Errorf("解析 Agent 更新清单失败: %w", err))
	}
	if manifest.SchemaVersion != 1 {
		return u.fail(fmt.Errorf("Agent 更新清单 schemaVersion 必须为 1，实际为 %d", manifest.SchemaVersion))
	}
	publishedAt, err := time.Parse(time.RFC3339, manifest.PublishedAt)
	if err != nil {
		return u.fail(fmt.Errorf("Agent 更新发布时间无效: %w", err))
	}
	newer, err := semverNewer(manifest.Version, buildinfo.Version)
	if err != nil {
		return u.fail(err)
	}
	if !newer {
		u.mu.Lock()
		u.pending = nil
		u.status.State = "up-to-date"
		u.status.LatestVersion = strings.TrimPrefix(manifest.Version, "v")
		u.status.PublishedAt = &publishedAt
		u.status.Notes = manifest.Notes
		u.status.Downloaded = 0
		u.status.Total = 0
		u.status.Error = ""
		status := u.status
		u.mu.Unlock()
		return status, nil
	}
	var selected *updateArtifact
	for index := range manifest.Artifacts {
		artifact := &manifest.Artifacts[index]
		if artifact.Component == "agent" && artifact.Platform == runtime.GOOS && artifact.Arch == runtime.GOARCH {
			if selected != nil {
				return u.fail(errors.New("Agent 更新清单包含重复的平台资产"))
			}
			selected = artifact
		}
	}
	if selected == nil {
		return u.fail(fmt.Errorf("Agent 更新清单缺少 %s/%s 资产", runtime.GOOS, runtime.GOARCH))
	}
	if err := validateAgentArtifact(*selected); err != nil {
		return u.fail(err)
	}
	u.mu.Lock()
	copy := *selected
	u.pending = &copy
	u.status.State = "available"
	u.status.LatestVersion = strings.TrimPrefix(manifest.Version, "v")
	u.status.PublishedAt = &publishedAt
	u.status.Notes = manifest.Notes
	u.status.Downloaded = 0
	u.status.Total = selected.Size
	u.status.Signature = "签名已声明，等待下载校验"
	u.status.Error = ""
	status := u.status
	u.mu.Unlock()
	return status, nil
}

func (u *AgentUpdater) Download(ctx context.Context) (AgentUpdateStatus, error) {
	u.mu.RLock()
	if u.pending == nil {
		u.mu.RUnlock()
		return u.fail(errors.New("尚未检查到 Agent 更新"))
	}
	artifact := *u.pending
	version := u.status.LatestVersion
	u.mu.RUnlock()
	u.mu.Lock()
	u.status.State = "downloading"
	u.status.Downloaded = 0
	u.status.Error = ""
	u.mu.Unlock()
	target, err := os.Executable()
	if err != nil {
		return u.fail(fmt.Errorf("定位 Agent 可执行文件失败: %w", err))
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(target), ".homestack-agent-update-*")
	if err != nil {
		return u.fail(fmt.Errorf("在 Agent 同文件系统创建暂存目录失败: %w", err))
	}
	archivePath := filepath.Join(stagingDir, artifact.Filename)
	if err := u.downloadArtifact(ctx, artifact, archivePath); err != nil {
		_ = os.RemoveAll(stagingDir)
		return u.fail(err)
	}
	u.setState("verifying", "")
	if err := verifyAgentArtifact(archivePath, artifact, u.publicKey); err != nil {
		_ = os.RemoveAll(stagingDir)
		return u.fail(err)
	}
	stagedPath := filepath.Join(stagingDir, "homestack-agent")
	if err := extractAgentArchive(archivePath, stagedPath); err != nil {
		_ = os.RemoveAll(stagingDir)
		return u.fail(err)
	}
	if err := validateAgentVersion(ctx, stagedPath, version); err != nil {
		_ = os.RemoveAll(stagingDir)
		return u.fail(err)
	}
	u.mu.Lock()
	u.stagedPath = stagedPath
	u.status.State = "ready"
	u.status.Signature = "Ed25519 与内嵌版本校验通过"
	u.status.Error = ""
	status := u.status
	u.mu.Unlock()
	return status, nil
}

func (u *AgentUpdater) Install() error {
	u.mu.RLock()
	staged := u.stagedPath
	version := u.status.LatestVersion
	u.mu.RUnlock()
	if staged == "" || version == "" {
		return errors.New("Agent 更新尚未完成校验")
	}
	target, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 Agent 可执行文件失败: %w", err)
	}
	backup := target + ".backup-" + version
	if _, err := os.Stat(backup); err == nil {
		return fmt.Errorf("Agent 更新备份已存在: %s", backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 Agent 更新备份失败: %w", err)
	}
	unit := "homestack-agent-update-" + strconv.FormatInt(time.Now().Unix(), 10)
	arguments := []string{
		"--user", "--collect", "--unit=" + unit, "--property=Type=exec", target, "update-helper",
		"--parent-pid=" + strconv.Itoa(os.Getpid()), "--target=" + target, "--staged=" + staged, "--backup=" + backup,
		"--health-url=" + u.agentURL + "/api/health",
	}
	output, err := exec.Command("systemd-run", arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("启动 Agent 更新 helper 失败: %s", strings.TrimSpace(string(output)))
	}
	u.setState("installing", "")
	return nil
}

func (u *AgentUpdater) downloadArtifact(ctx context.Context, artifact updateArtifact, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return err
	}
	response, err := releaseproxy.NewClient(20 * time.Minute).Do(request)
	if err != nil {
		return fmt.Errorf("下载 Agent 更新失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 Agent 更新失败: HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("创建 Agent 更新暂存文件失败: %w", err)
	}
	progress := &updateProgressWriter{destination: file, onProgress: func(written int64) {
		u.mu.Lock()
		u.status.Downloaded = written
		u.mu.Unlock()
	}}
	written, copyErr := io.Copy(progress, io.LimitReader(response.Body, artifact.Size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("下载 Agent 更新失败: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭 Agent 更新暂存文件失败: %w", closeErr)
	}
	if written != artifact.Size {
		return fmt.Errorf("Agent 更新大小不匹配: 预期 %d，实际 %d", artifact.Size, written)
	}
	return nil
}

func validateAgentArtifact(artifact updateArtifact) error {
	if !releaseproxy.IsOfficialURL(artifact.URL) {
		return errors.New("Agent 更新资产必须使用批准的 HomeStack Release 地址")
	}
	if artifact.Filename == "" || artifact.Size <= 0 || artifact.Size > 512<<20 || artifact.DigestAlgo != "sha256" || artifact.SignatureAlgo != "ed25519" || artifact.Digest == "" || artifact.Signature == "" {
		return errors.New("Agent 更新资产的文件名、大小、摘要或签名字段无效")
	}
	return nil
}

func verifyAgentArtifact(path string, artifact updateArtifact, publicKey ed25519.PublicKey) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 Agent 更新资产失败: %w", err)
	}
	digest := sha256.Sum256(data)
	expectedDigest, err := decodeDigest(artifact.Digest)
	if err != nil || len(expectedDigest) != sha256.Size || !strings.EqualFold(hex.EncodeToString(digest[:]), hex.EncodeToString(expectedDigest)) {
		return errors.New("Agent 更新 SHA-256 校验失败")
	}
	signature, err := decodeBase64(artifact.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, digest[:], signature) {
		return errors.New("Agent 更新 Ed25519 签名校验失败")
	}
	return nil
}

func extractAgentArchive(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("打开 Agent 更新 gzip 失败: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	header, err := tarReader.Next()
	if err != nil {
		return fmt.Errorf("读取 Agent 更新归档失败: %w", err)
	}
	if header.Name != "homestack-agent" || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 512<<20 {
		return errors.New("Agent 更新归档必须只含单顶层 homestack-agent 常规文件")
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return fmt.Errorf("创建 Agent 暂存程序失败: %w", err)
	}
	written, copyErr := io.Copy(output, io.LimitReader(tarReader, header.Size+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil || written != header.Size {
		return errors.New("解包 Agent 暂存程序失败或大小不匹配")
	}
	if _, err := tarReader.Next(); !errors.Is(err, io.EOF) {
		return errors.New("Agent 更新归档包含额外条目")
	}
	return nil
}

func validateAgentVersion(ctx context.Context, path, expected string) error {
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandContext, path, "--version-json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("执行 Agent 暂存程序失败: %s", strings.TrimSpace(string(output)))
	}
	return validateAgentVersionOutput(output, expected, runtime.GOOS, runtime.GOARCH)
}

func validateAgentVersionOutput(output []byte, expected, expectedGOOS, expectedGOARCH string) error {
	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		GOOS    string `json:"goos"`
		GOARCH  string `json:"goarch"`
	}
	if err := json.Unmarshal(output, &version); err != nil {
		return fmt.Errorf("解析 Agent 暂存版本失败: %w", err)
	}
	if version.Name != "homestack-agent" || strings.TrimPrefix(version.Version, "v") != strings.TrimPrefix(expected, "v") || version.GOOS != expectedGOOS || version.GOARCH != expectedGOARCH {
		return fmt.Errorf("Agent 暂存版本、平台或架构不匹配: %s %s %s/%s", version.Name, version.Version, version.GOOS, version.GOARCH)
	}
	return nil
}

func decodeAgentUpdatePublicKey(raw string) (ed25519.PublicKey, error) {
	decoded, err := decodeBase64(raw)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("Agent 更新 Ed25519 公钥编码无效")
	}
	return ed25519.PublicKey(decoded), nil
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

func (u *AgentUpdater) setState(state, message string) {
	u.mu.Lock()
	u.status.State = state
	u.status.Error = message
	u.mu.Unlock()
}

func (u *AgentUpdater) fail(err error) (AgentUpdateStatus, error) {
	u.setState("error", err.Error())
	return u.Status(), err
}
