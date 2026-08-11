package managed

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ulikunitz/xz"
)

const (
	maxExtractedSize            int64 = 2 << 30
	componentDownloadAttempts         = 12
	componentDownloadRetryDelay       = 250 * time.Millisecond
)

var installLock sync.Mutex

type Installation struct {
	Component      string `json:"component"`
	Version        string `json:"version"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	SourceHost     string `json:"source_host,omitempty"`
	Executable     string `json:"executable"`
	Root           string `json:"root"`
	WebDir         string `json:"web_dir,omitempty"`
	FFmpeg         string `json:"ffmpeg,omitempty"`
}

type Installer struct {
	Client   *http.Client
	Root     string
	Progress ProgressFunc
}

func (i Installer) Ensure(ctx context.Context, artifact Artifact) (Installation, error) {
	installLock.Lock()
	defer installLock.Unlock()

	if i.Client == nil || !filepath.IsAbs(i.Root) {
		err := errors.New("托管组件安装器配置无效")
		i.report(artifact, PhaseError, 0, artifact.Size, 0, "", err.Error())
		return Installation{}, err
	}
	digest, err := hex.DecodeString(artifact.SHA256)
	if err != nil || len(digest) != sha256.Size {
		err := errors.New("托管组件资产 SHA-256 无效")
		i.report(artifact, PhaseError, 0, artifact.Size, 0, "", err.Error())
		return Installation{}, err
	}
	root := filepath.Join(i.Root, artifact.Component, artifact.Version, runtime.GOOS+"-"+runtime.GOARCH+"-"+hex.EncodeToString(digest[:8]))
	marker := filepath.Join(root, "installation.json")
	if installed, err := loadInstallation(marker, artifact); err == nil {
		i.report(artifact, PhaseReady, artifact.Size, artifact.Size, 0, installed.SourceHost, "")
		return installed, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return i.installationError(artifact, "", err)
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return i.installationError(artifact, "", fmt.Errorf("创建组件目录失败: %w", err))
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".install-*")
	if err != nil {
		return i.installationError(artifact, "", fmt.Errorf("创建组件暂存目录失败: %w", err))
	}
	defer os.RemoveAll(stage)
	archivePath := filepath.Join(stage, artifact.Filename)
	sourceURL, sourceHost, err := selectSource(ctx, i.Client, artifact, i.Progress)
	if err != nil {
		i.report(artifact, PhaseError, 0, artifact.Size, 0, "", err.Error())
		return Installation{}, err
	}
	if err := downloadArtifact(ctx, i.Client, artifact, sourceURL, sourceHost, archivePath, i.Progress); err != nil {
		i.report(artifact, PhaseError, 0, artifact.Size, 0, sourceHost, err.Error())
		return Installation{}, err
	}
	payload := filepath.Join(stage, "payload")
	if err := os.Mkdir(payload, 0o700); err != nil {
		return i.installationError(artifact, sourceHost, err)
	}
	i.report(artifact, PhaseExtracting, artifact.Size, artifact.Size, 0, sourceHost, "")
	if err := extractArtifact(archivePath, payload, artifact.Format); err != nil {
		return i.installationError(artifact, sourceHost, err)
	}
	if err := ctx.Err(); err != nil {
		return i.installationError(artifact, sourceHost, err)
	}
	installed, err := inspectInstallation(payload, artifact)
	if err != nil {
		return i.installationError(artifact, sourceHost, err)
	}
	installed.Root = root
	installed.SourceHost = sourceHost
	installed.Executable = relocatePath(installed.Executable, payload, root)
	installed.WebDir = relocatePath(installed.WebDir, payload, root)
	installed.FFmpeg = relocatePath(installed.FFmpeg, payload, root)
	markerData, err := json.Marshal(installed)
	if err != nil {
		return i.installationError(artifact, sourceHost, err)
	}
	if err := os.WriteFile(filepath.Join(payload, "installation.json"), markerData, 0o600); err != nil {
		return i.installationError(artifact, sourceHost, fmt.Errorf("写入组件安装标记失败: %w", err))
	}
	if _, err := os.Stat(root); err == nil {
		return i.installationError(artifact, sourceHost, fmt.Errorf("组件安装目标已存在但标记无效: %s", root))
	} else if !errors.Is(err, os.ErrNotExist) {
		return i.installationError(artifact, sourceHost, err)
	}
	i.report(artifact, PhaseInstalling, artifact.Size, artifact.Size, 0, sourceHost, "")
	if err := os.Rename(payload, root); err != nil {
		return i.installationError(artifact, sourceHost, fmt.Errorf("提交组件安装失败: %w", err))
	}
	i.report(artifact, PhaseReady, artifact.Size, artifact.Size, 0, sourceHost, "")
	return installed, nil
}

func (i Installer) installationError(artifact Artifact, sourceHost string, err error) (Installation, error) {
	i.report(artifact, PhaseError, 0, artifact.Size, 0, sourceHost, err.Error())
	return Installation{}, err
}

func (i Installer) report(artifact Artifact, phase string, downloaded, total, speed int64, sourceHost, detail string) {
	if i.Progress != nil {
		i.Progress(Progress{Component: artifact.Component, Version: artifact.Version, Phase: phase, Downloaded: downloaded, Total: total, SpeedBPS: speed, SourceHost: sourceHost, Error: detail})
	}
}

func loadInstallation(path string, artifact Artifact) (Installation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Installation{}, err
	}
	var installed Installation
	if err := json.Unmarshal(data, &installed); err != nil {
		return Installation{}, fmt.Errorf("解析组件安装标记失败: %w", err)
	}
	if installed.Component != artifact.Component || installed.Version != artifact.Version || !strings.EqualFold(installed.ArtifactSHA256, artifact.SHA256) || !filepath.IsAbs(installed.Executable) {
		return Installation{}, errors.New("组件安装标记与清单不匹配")
	}
	info, err := os.Stat(installed.Executable)
	if err != nil || !info.Mode().IsRegular() {
		return Installation{}, errors.New("组件安装标记指向的可执行文件不存在")
	}
	return installed, nil
}

func downloadArtifact(ctx context.Context, client *http.Client, artifact Artifact, sourceURL, sourceHost, target string, report ProgressFunc) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	offset := int64(0)
	downloadStarted := time.Now()
	if report != nil {
		report(Progress{Component: artifact.Component, Version: artifact.Version, Phase: PhaseDownloading, Total: artifact.Size, SourceHost: sourceHost})
	}
	for attempt := 1; offset < artifact.Size; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
		if requestErr != nil {
			_ = file.Close()
			return requestErr
		}
		if offset > 0 {
			request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			if attempt >= componentDownloadAttempts {
				_ = file.Close()
				return fmt.Errorf("下载 %s 失败（已接收 %d/%d 字节，尝试 %d 次）: %w", artifact.Component, offset, artifact.Size, attempt, requestErr)
			}
			if err := waitForDownloadRetry(ctx, attempt); err != nil {
				_ = file.Close()
				return err
			}
			continue
		}
		expectedStatus := http.StatusOK
		if offset > 0 {
			expectedStatus = http.StatusPartialContent
		}
		if response.StatusCode != expectedStatus {
			_ = response.Body.Close()
			_ = file.Close()
			return fmt.Errorf("下载 %s 失败: HTTP %d", artifact.Component, response.StatusCode)
		}
		if offset > 0 {
			expectedRange := fmt.Sprintf("bytes %d-%d/%d", offset, artifact.Size-1, artifact.Size)
			if response.Header.Get("Content-Range") != expectedRange {
				_ = response.Body.Close()
				_ = file.Close()
				return fmt.Errorf("下载 %s 的 Content-Range 无效", artifact.Component)
			}
		}
		remaining := artifact.Size - offset
		progress := &progressWriter{written: offset, total: artifact.Size, started: downloadStarted}
		progress.report = func(downloaded, speed int64) {
			if report != nil {
				report(Progress{Component: artifact.Component, Version: artifact.Version, Phase: PhaseDownloading, Downloaded: downloaded, Total: artifact.Size, SpeedBPS: speed, SourceHost: sourceHost})
			}
		}
		written, copyErr := io.Copy(io.MultiWriter(file, hash, progress), io.LimitReader(response.Body, remaining+1))
		closeErr := response.Body.Close()
		offset += written
		if written > remaining {
			_ = file.Close()
			return fmt.Errorf("%s 下载大小超出清单限制", artifact.Component)
		}
		if offset == artifact.Size {
			break
		}
		if closeErr != nil && copyErr == nil {
			copyErr = closeErr
		}
		if copyErr == nil {
			copyErr = io.ErrUnexpectedEOF
		}
		if attempt >= componentDownloadAttempts {
			_ = file.Close()
			return fmt.Errorf("保存 %s 失败（已接收 %d/%d 字节，尝试 %d 次）: %w", artifact.Component, offset, artifact.Size, attempt, copyErr)
		}
		if err := waitForDownloadRetry(ctx, attempt); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("同步 %s 下载文件失败: %w", artifact.Component, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 %s 下载文件失败: %w", artifact.Component, err)
	}
	if report != nil {
		report(Progress{Component: artifact.Component, Version: artifact.Version, Phase: PhaseVerifying, Downloaded: artifact.Size, Total: artifact.Size, SourceHost: sourceHost})
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actual, artifact.SHA256) {
		return fmt.Errorf("%s SHA-256 校验失败", artifact.Component)
	}
	return nil
}

func waitForDownloadRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt) * componentDownloadRetryDelay
	if delay > 2*time.Second {
		delay = 2 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func extractArtifact(archivePath, destination, format string) error {
	switch format {
	case "binary":
		name := "filebrowser"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return copyExecutable(archivePath, filepath.Join(destination, name))
	case "zip":
		return extractZip(archivePath, destination)
	case "tar.gz", "tar.xz":
		return extractTar(archivePath, destination, format)
	default:
		return fmt.Errorf("不支持的组件归档格式: %s", format)
	}
}

func extractZip(path, destination string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("打开组件 ZIP 失败: %w", err)
	}
	defer reader.Close()
	var extracted int64
	for index, entry := range reader.File {
		if index >= 100_000 {
			return errors.New("组件 ZIP 条目数量超出限制")
		}
		if entry.UncompressedSize64 > uint64(maxExtractedSize-extracted) {
			return fmt.Errorf("组件 ZIP 解压大小超出限制: %s", entry.Name)
		}
		if err := writeArchiveEntry(destination, entry.Name, entry.Mode(), func(writer io.Writer) error {
			input, err := entry.Open()
			if err != nil {
				return err
			}
			defer input.Close()
			written, err := io.Copy(writer, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
			if err != nil {
				return err
			}
			if written != int64(entry.UncompressedSize64) {
				return fmt.Errorf("组件 ZIP 条目大小不匹配: %s", entry.Name)
			}
			extracted += written
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func extractTar(path, destination, format string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var input io.Reader = file
	if format == "tar.gz" {
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("打开组件 Gzip 失败: %w", err)
		}
		defer gzipReader.Close()
		input = gzipReader
	} else {
		xzReader, err := xz.NewReader(file)
		if err != nil {
			return fmt.Errorf("打开组件 XZ 失败: %w", err)
		}
		input = xzReader
	}
	target := tar.NewReader(input)
	var extracted int64
	entries := 0
	for {
		header, err := target.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读取组件 TAR 失败: %w", err)
		}
		entries++
		if entries > 100_000 {
			return errors.New("组件 TAR 条目数量超出限制")
		}
		if header.Size < 0 || header.Size > maxExtractedSize-extracted {
			return fmt.Errorf("组件 TAR 解压大小超出限制: %s", header.Name)
		}
		mode := os.FileMode(header.Mode)
		if header.Typeflag == tar.TypeDir {
			mode |= os.ModeDir
		} else if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("组件归档包含不允许的条目: %s", header.Name)
		}
		if err := writeArchiveEntry(destination, header.Name, mode, func(writer io.Writer) error {
			written, err := io.CopyN(writer, target, header.Size)
			extracted += written
			return err
		}); err != nil {
			return err
		}
	}
}

func writeArchiveEntry(root, name string, mode os.FileMode, write func(io.Writer) error) error {
	name = filepath.Clean(filepath.FromSlash(name))
	if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return fmt.Errorf("组件归档路径越界: %s", name)
	}
	target := filepath.Join(root, name)
	if mode.IsDir() {
		return os.MkdirAll(target, 0o700)
	}
	if !mode.IsRegular() {
		return fmt.Errorf("组件归档包含特殊文件: %s", name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	permissions := os.FileMode(0o600)
	if mode&0o111 != 0 {
		permissions = 0o700
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
	if err != nil {
		return err
	}
	writeErr := write(file)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func copyExecutable(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	return writeArchiveEntry(filepath.Dir(target), filepath.Base(target), 0o700, func(writer io.Writer) error {
		_, err := io.Copy(writer, input)
		return err
	})
}

func inspectInstallation(root string, artifact Artifact) (Installation, error) {
	installed := Installation{Component: artifact.Component, Version: artifact.Version, ArtifactSHA256: strings.ToLower(artifact.SHA256)}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := strings.ToLower(entry.Name())
		if entry.IsDir() && name == "jellyfin-web" {
			installed.WebDir = path
			return nil
		}
		if entry.Type().IsRegular() {
			if artifact.Component == "filebrowser" && (name == "filebrowser" || name == "filebrowser.exe") {
				installed.Executable = path
			}
			if artifact.Component == "jellyfin" && (name == "jellyfin" || name == "jellyfin.exe") {
				installed.Executable = path
			}
			if artifact.Component == "jellyfin-ffmpeg" && (name == "ffmpeg" || name == "ffmpeg.exe") {
				installed.Executable = path
			}
			if name == "ffmpeg" || name == "ffmpeg.exe" {
				installed.FFmpeg = path
			}
		}
		return nil
	})
	if err != nil {
		return Installation{}, err
	}
	if installed.Executable == "" {
		return Installation{}, fmt.Errorf("%s 归档缺少可执行文件", artifact.Component)
	}
	if artifact.Component == "jellyfin" && installed.WebDir == "" {
		return Installation{}, errors.New("Jellyfin 归档缺少 jellyfin-web")
	}
	return installed, nil
}

func relocatePath(path, oldRoot, newRoot string) string {
	if path == "" {
		return ""
	}
	relative, _ := filepath.Rel(oldRoot, path)
	return filepath.Join(newRoot, relative)
}
