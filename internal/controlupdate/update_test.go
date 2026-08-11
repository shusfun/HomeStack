package controlupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

type testInstaller struct {
	request setupapi.ControlUpdateInstallation
	err     error
}

func (i *testInstaller) InstallControlUpdate(_ context.Context, request setupapi.ControlUpdateInstallation) error {
	i.request = request
	return i.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestUpdaterChecksDownloadsAndInstallsSignedControlAsset(t *testing.T) {
	archive := []byte("signed control update")
	digest := sha256.Sum256(archive)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	filename := "homestack-control-update_1.2.4_linux_amd64.tar.gz"
	assetURL := "https://github.com/shusfun/HomeStack/releases/download/v1.2.4/" + filename
	manifestURL := "https://gh-proxy.com/https://github.com/shusfun/HomeStack/releases/latest/download/latest.json"
	value := manifest{
		SchemaVersion: 1, Version: "1.2.4", Channel: "stable", Name: "HomeStack 1.2.4",
		PublishedAt: time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC).Format(time.RFC3339), Notes: "release",
		Artifacts: []artifact{{
			Component: "control", URL: assetURL, Filename: filename, Filetype: "tar.gz", Size: int64(len(archive)), Platform: "linux", Arch: "amd64",
			DigestAlgo: "sha256", Digest: base64.StdEncoding.EncodeToString(digest[:]), SignatureAlgo: "ed25519", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digest[:])),
		}},
	}
	manifestData, _ := json.Marshal(value)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var data []byte
		switch request.URL.String() {
		case manifestURL:
			data = manifestData
		case assetURL:
			data = archive
		default:
			t.Fatalf("意外请求: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header), Request: request}, nil
	})}
	installer := &testInstaller{}
	stateDir := t.TempDir()
	updater, err := New(Options{CurrentVersion: "1.2.3", ManifestURL: manifestURL, PublicKey: base64.StdEncoding.EncodeToString(publicKey), StateDir: stateDir, Platform: "linux", Architecture: "amd64", HTTPClient: client, Installer: installer})
	if err != nil {
		t.Fatal(err)
	}
	status, err := updater.Check(t.Context())
	if err != nil || status.State != "available" || status.LatestVersion != "1.2.4" {
		t.Fatalf("检查状态错误: %+v err=%v", status, err)
	}
	status, err = updater.Download(t.Context())
	if err != nil || status.State != "ready" || status.Downloaded != int64(len(archive)) {
		t.Fatalf("下载状态错误: %+v err=%v", status, err)
	}
	status, err = updater.Install(t.Context())
	if err != nil || status.State != "installing" || installer.request.Filename != filename || installer.request.Version != "1.2.4" {
		t.Fatalf("安装状态错误: %+v request=%+v err=%v", status, installer.request, err)
	}
	expectedPath := filepath.Join(stateDir, "updates", "1.2.4", filename)
	if installer.request.ArchivePath != expectedPath {
		t.Fatalf("暂存路径错误: %s", installer.request.ArchivePath)
	}
	if data, err := os.ReadFile(expectedPath); err != nil || !bytes.Equal(data, archive) {
		t.Fatalf("暂存资产错误: %v", err)
	}
}

func TestUpdaterRejectsTamperedControlAsset(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	digest := sha256.Sum256([]byte("expected"))
	current := artifact{Digest: base64.StdEncoding.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digest[:])), Size: int64(len("tampered"))}
	path := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyArtifact(path, current, publicKey); err == nil {
		t.Fatal("篡改的 Control 更新资产未被拒绝")
	}
}

func TestStatusOmitsUnknownPublicationTime(t *testing.T) {
	data, err := json.Marshal(Status{State: "idle", CurrentVersion: "1.2.3", Signature: "等待校验"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("published_at")) {
		t.Fatalf("未知发布时间不应进入 API: %s", data)
	}
}

func TestUpdaterReportsReleaseMetadataWhenCurrent(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publishedAt := time.Date(2026, 8, 11, 11, 31, 13, 0, time.UTC)
	manifestURL := "https://gh-proxy.com/https://github.com/shusfun/HomeStack/releases/latest/download/latest.json"
	manifestData, err := json.Marshal(manifest{
		SchemaVersion: 1,
		Version:       "1.2.4",
		PublishedAt:   publishedAt.Format(time.RFC3339),
		Notes:         "release notes",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(manifestData)), Header: make(http.Header), Request: request}, nil
	})}
	updater, err := New(Options{
		CurrentVersion: "1.2.4", ManifestURL: manifestURL, PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		StateDir: t.TempDir(), Platform: "linux", Architecture: "amd64", HTTPClient: client, Installer: &testInstaller{},
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := updater.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "up-to-date" || status.LatestVersion != "1.2.4" || status.PublishedAt == nil || !status.PublishedAt.Equal(publishedAt) || status.Notes != "release notes" {
		t.Fatalf("当前版本的 Release 元数据不完整: %+v", status)
	}
}

func TestValidateArtifactRequiresExactControlUpdateName(t *testing.T) {
	valid := artifact{
		URL:      "https://github.com/shusfun/HomeStack/releases/download/v1.2.4/homestack-control-update_1.2.4_linux_arm64.tar.gz",
		Filename: "homestack-control-update_1.2.4_linux_arm64.tar.gz", Size: 10, DigestAlgo: "sha256", Digest: "digest", SignatureAlgo: "ed25519", Signature: "signature",
	}
	if err := validateArtifact(valid, "1.2.4", "arm64"); err != nil {
		t.Fatal(err)
	}
	valid.Filename = "homestack-control_1.2.4_linux_arm64.tar.gz"
	if err := validateArtifact(valid, "1.2.4", "arm64"); err == nil {
		t.Fatal("安装归档不能冒充 Control 更新归档")
	}
}
