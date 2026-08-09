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

	"github.com/ulikunitz/xz"
)

const maxExtractedSize int64 = 2 << 30

type Installation struct {
	Component      string `json:"component"`
	Version        string `json:"version"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	Executable     string `json:"executable"`
	Root           string `json:"root"`
	WebDir         string `json:"web_dir,omitempty"`
	FFmpeg         string `json:"ffmpeg,omitempty"`
}

type Installer struct {
	Client *http.Client
	Root   string
}

func (i Installer) Ensure(ctx context.Context, artifact Artifact) (Installation, error) {
	if i.Client == nil || !filepath.IsAbs(i.Root) {
		return Installation{}, errors.New("托管组件安装器配置无效")
	}
	digest, err := hex.DecodeString(artifact.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return Installation{}, errors.New("托管组件资产 SHA-256 无效")
	}
	root := filepath.Join(i.Root, artifact.Component, artifact.Version, runtime.GOOS+"-"+runtime.GOARCH+"-"+hex.EncodeToString(digest[:8]))
	marker := filepath.Join(root, "installation.json")
	if installed, err := loadInstallation(marker, artifact); err == nil {
		return installed, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Installation{}, err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return Installation{}, fmt.Errorf("创建组件目录失败: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".install-*")
	if err != nil {
		return Installation{}, fmt.Errorf("创建组件暂存目录失败: %w", err)
	}
	defer os.RemoveAll(stage)
	archivePath := filepath.Join(stage, artifact.Filename)
	if err := downloadArtifact(ctx, i.Client, artifact, archivePath); err != nil {
		return Installation{}, err
	}
	payload := filepath.Join(stage, "payload")
	if err := os.Mkdir(payload, 0o700); err != nil {
		return Installation{}, err
	}
	if err := extractArtifact(archivePath, payload, artifact.Format); err != nil {
		return Installation{}, err
	}
	installed, err := inspectInstallation(payload, artifact)
	if err != nil {
		return Installation{}, err
	}
	installed.Root = root
	installed.Executable = relocatePath(installed.Executable, payload, root)
	installed.WebDir = relocatePath(installed.WebDir, payload, root)
	installed.FFmpeg = relocatePath(installed.FFmpeg, payload, root)
	markerData, err := json.Marshal(installed)
	if err != nil {
		return Installation{}, err
	}
	if err := os.WriteFile(filepath.Join(payload, "installation.json"), markerData, 0o600); err != nil {
		return Installation{}, fmt.Errorf("写入组件安装标记失败: %w", err)
	}
	if _, err := os.Stat(root); err == nil {
		return Installation{}, fmt.Errorf("组件安装目标已存在但标记无效: %s", root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Installation{}, err
	}
	if err := os.Rename(payload, root); err != nil {
		return Installation{}, fmt.Errorf("提交组件安装失败: %w", err)
	}
	return installed, nil
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

func downloadArtifact(ctx context.Context, client *http.Client, artifact Artifact, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", artifact.Component, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 失败: HTTP %d", artifact.Component, response.StatusCode)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, artifact.Size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("保存 %s 失败: %w", artifact.Component, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭 %s 下载文件失败: %w", artifact.Component, closeErr)
	}
	if written != artifact.Size {
		return fmt.Errorf("%s 下载大小不匹配: 期望 %d，实际 %d", artifact.Component, artifact.Size, written)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actual, artifact.SHA256) {
		return fmt.Errorf("%s SHA-256 校验失败", artifact.Component)
	}
	return nil
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
