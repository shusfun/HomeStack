package setuphelper

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

func TestManagerInstallsSignedControlUpdateAndFinalizes(t *testing.T) {
	manager, update := controlUpdateFixture(t)
	commands := make([]string, 0, 3)
	manager.Command = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(arguments, " "))
		return nil, nil
	}
	healthChecks := 0
	manager.HealthCheck = func(context.Context) error {
		healthChecks++
		return nil
	}
	if err := manager.InstallControlUpdate(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, manager.ControlBinary)) != "new-control" || string(mustRead(t, manager.ConfigHelperBinary)) != "new-helper" {
		t.Fatal("Control 更新二进制未安装")
	}
	transaction := filepath.Join(manager.ControlUpdateWork, update.Version, "transaction.json")
	if err := manager.FinalizeControlUpdate(t.Context(), transaction); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 4 || !strings.HasPrefix(commands[0], "systemd-run --collect --unit=homestack-control-update ") || strings.Contains(commands[0], "--replace") || commands[1] != "systemctl restart homestack-control.service" || commands[2] != "systemctl restart homestack-config-helper.service" || commands[3] != "systemctl restart homestack-control.service" {
		t.Fatalf("Control 更新命令错误: %v", commands)
	}
	if healthChecks != 2 {
		t.Fatalf("重启 Helper 前后必须分别检查 Control 健康状态: %d", healthChecks)
	}
	if _, err := os.Stat(filepath.Dir(transaction)); !os.IsNotExist(err) {
		t.Fatalf("成功事务未清理: %v", err)
	}
}

func TestManagerRollsBackFailedControlHealthCheck(t *testing.T) {
	manager, update := controlUpdateFixture(t)
	manager.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	if err := manager.InstallControlUpdate(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	manager.HealthCheck = func(context.Context) error { return fmt.Errorf("unhealthy") }
	transaction := filepath.Join(manager.ControlUpdateWork, update.Version, "transaction.json")
	if err := manager.FinalizeControlUpdate(t.Context(), transaction); err == nil || !strings.Contains(err.Error(), "已回滚") {
		t.Fatalf("健康检查失败未返回回滚错误: %v", err)
	}
	if string(mustRead(t, manager.ControlBinary)) != "old-control" || string(mustRead(t, manager.ConfigHelperBinary)) != "old-helper" {
		t.Fatal("健康检查失败未恢复旧二进制")
	}
	if _, err := os.Stat(filepath.Dir(transaction)); !os.IsNotExist(err) {
		t.Fatalf("完整回滚后事务未清理，无法重试同一版本: %v", err)
	}
}

func TestManagerRejectsTamperedOrEscapedControlUpdate(t *testing.T) {
	manager, update := controlUpdateFixture(t)
	manager.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	update.ArchivePath = filepath.Join(t.TempDir(), update.Filename)
	if err := manager.InstallControlUpdate(t.Context(), update); err == nil || !strings.Contains(err.Error(), "受控目录") {
		t.Fatalf("受控目录外资产未被拒绝: %v", err)
	}
	manager, update = controlUpdateFixture(t)
	file, err := os.OpenFile(update.ArchivePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("tampered")
	_ = file.Close()
	if err := manager.InstallControlUpdate(t.Context(), update); err == nil || !strings.Contains(err.Error(), "大小校验") {
		t.Fatalf("篡改资产未被拒绝: %v", err)
	}
}

func controlUpdateFixture(t *testing.T) (*Manager, setupapi.ControlUpdateInstallation) {
	t.Helper()
	directory := t.TempDir()
	root := filepath.Join(directory, "downloads")
	work := filepath.Join(directory, "transactions")
	bin := filepath.Join(directory, "bin")
	libexec := filepath.Join(directory, "libexec")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(libexec, 0o700); err != nil {
		t.Fatal(err)
	}
	controlTarget := filepath.Join(bin, "homestack-control")
	helperTarget := filepath.Join(libexec, "homestack-config-helper")
	if err := os.WriteFile(controlTarget, []byte("old-control"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperTarget, []byte("old-helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	version := "1.2.4"
	filename := "homestack-control-update_1.2.4_linux_amd64.tar.gz"
	archiveDirectory := filepath.Join(root, version)
	if err := os.MkdirAll(archiveDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(archiveDirectory, filename)
	archive := controlUpdateArchive(t, map[string]string{"homestack-control": "new-control", "homestack-config-helper": "new-helper"})
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	manager := &Manager{
		ControlUpdateRoot: root, ControlUpdateWork: work, ControlBinary: controlTarget, ConfigHelperBinary: helperTarget,
		UpdatePublicKey: base64.StdEncoding.EncodeToString(publicKey), Platform: "linux", Architecture: "amd64",
		Now: func() time.Time { return time.Now() }, Chown: func(string, ...string) error { return nil },
		RunVersion: func(_ context.Context, path string) ([]byte, error) {
			name := "homestack-control"
			if strings.Contains(filepath.Base(path), "config-helper") {
				name = "homestack-config-helper"
			}
			return []byte(fmt.Sprintf(`{"name":%q,"version":"1.2.4","goos":"linux","goarch":"amd64"}`, name)), nil
		},
	}
	update := setupapi.ControlUpdateInstallation{
		Version: version, ArchivePath: archivePath, Filename: filename, Size: int64(len(archive)),
		Digest: base64.StdEncoding.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digest[:])),
	}
	return manager, update
}

func controlUpdateArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for name, content := range entries {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o700, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
