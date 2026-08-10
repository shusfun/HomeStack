package managed

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstallerRejectsDigestAndArchiveTraversal(t *testing.T) {
	archive := zipBytes(t, "../escape", []byte("bad"))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(archive) }))
	defer server.Close()
	artifact := Artifact{Component: "jellyfin", Version: "10.11.11", Platform: runtime.GOOS, Arch: runtime.GOARCH, URL: server.URL, Filename: "jellyfin.zip", Format: "zip", Size: int64(len(archive)), SHA256: fmt.Sprintf("%x", sha256.Sum256(archive))}
	installer := Installer{Client: server.Client(), Root: t.TempDir()}
	if _, err := installer.Ensure(context.Background(), artifact); err == nil {
		t.Fatal("ZIP 路径穿越未被拒绝")
	}
	artifact.SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := installer.Ensure(context.Background(), artifact); err == nil {
		t.Fatal("错误组件摘要未被拒绝")
	}
}

func TestInstallerIsIdempotentForBinary(t *testing.T) {
	data := []byte("filebrowser")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { requests++; _, _ = writer.Write(data) }))
	defer server.Close()
	artifact := Artifact{Component: "filebrowser", Version: "0.3.5", Platform: runtime.GOOS, Arch: runtime.GOARCH, URL: server.URL, Filename: "filebrowser", Format: "binary", Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	installer := Installer{Client: server.Client(), Root: t.TempDir()}
	first, err := installer.Ensure(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	second, err := installer.Ensure(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || first.Executable != second.Executable {
		t.Fatalf("重复安装未复用现有组件: requests=%d first=%+v second=%+v", requests, first, second)
	}
	if info, err := os.Stat(first.Executable); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("组件可执行权限错误: %v %v", info, err)
	}
}

func TestDownloadArtifactResumesInterruptedResponse(t *testing.T) {
	data := bytes.Repeat([]byte("homestack-component"), 1024)
	cut := len(data) / 3
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		current := requests.Add(1)
		if current == 1 {
			writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(data[:cut])
			connection, _, err := writer.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("中断测试连接失败: %v", err)
				return
			}
			_ = connection.Close()
			return
		}
		expectedRange := fmt.Sprintf("bytes=%d-", cut)
		if request.Header.Get("Range") != expectedRange {
			t.Errorf("续传 Range 错误: %q", request.Header.Get("Range"))
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(data)-1, len(data)))
		writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)-cut))
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write(data[cut:])
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "component.bin")
	artifact := Artifact{Component: "jellyfin-ffmpeg", URL: server.URL, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	if err := downloadArtifact(context.Background(), server.Client(), artifact, target); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, data) || requests.Load() != 2 {
		t.Fatalf("断点续传结果错误: size=%d requests=%d", len(actual), requests.Load())
	}
}

func TestInspectInstallationFindsManagedFFmpeg(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bin", "ffmpeg")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("ffmpeg"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := Artifact{Component: "jellyfin-ffmpeg", Version: FFmpegVersion, SHA256: strings.Repeat("a", 64)}
	installed, err := inspectInstallation(root, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Executable != path {
		t.Fatalf("未定位托管 FFmpeg: %s", installed.Executable)
	}
}

func TestInstallerSerializesConcurrentInstall(t *testing.T) {
	data := []byte("filebrowser")
	var requests atomic.Int32
	firstRequest := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(firstRequest)
			<-release
		}
		_, _ = writer.Write(data)
	}))
	defer server.Close()

	artifact := Artifact{Component: "filebrowser", Version: "0.3.5", Platform: runtime.GOOS, Arch: runtime.GOARCH, URL: server.URL, Filename: "filebrowser", Format: "binary", Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	installer := Installer{Client: server.Client(), Root: t.TempDir()}
	results := make(chan Installation, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		installed, err := installer.Ensure(context.Background(), artifact)
		results <- installed
		errors <- err
	}()
	<-firstRequest
	go func() {
		defer workers.Done()
		installed, err := installer.Ensure(context.Background(), artifact)
		results <- installed
		errors <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	workers.Wait()
	close(results)
	close(errors)

	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var executable string
	for installed := range results {
		if executable == "" {
			executable = installed.Executable
		} else if installed.Executable != executable {
			t.Fatalf("并发安装未复用同一组件: first=%s second=%s", executable, installed.Executable)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("并发安装重复下载组件: requests=%d", requests.Load())
	}
}

func zipBytes(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	writer, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestRelocatePath(t *testing.T) {
	oldRoot := filepath.Join(string(filepath.Separator), "tmp", "old")
	newRoot := filepath.Join(string(filepath.Separator), "tmp", "new")
	if got := relocatePath(filepath.Join(oldRoot, "bin", "jellyfin"), oldRoot, newRoot); got != filepath.Join(newRoot, "bin", "jellyfin") {
		t.Fatalf("路径迁移错误: %s", got)
	}
}
