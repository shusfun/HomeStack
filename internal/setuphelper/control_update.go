package setuphelper

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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

var controlUpdateVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[.-][A-Za-z0-9.-]+)?$`)

type controlUpdateTransaction struct {
	Version             string `json:"version"`
	ControlTarget       string `json:"control_target"`
	HelperTarget        string `json:"helper_target"`
	ControlBackup       string `json:"control_backup"`
	HelperBackup        string `json:"helper_backup"`
	ControlUpdateFolder string `json:"control_update_folder"`
}

func (m *Manager) InstallControlUpdate(ctx context.Context, update setupapi.ControlUpdateInstallation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateControlUpdateRequest(update); err != nil {
		return err
	}
	archive, err := openStagedArchive(update.ArchivePath)
	if err != nil {
		return fmt.Errorf("打开 Control 更新暂存资产失败: %w", err)
	}
	defer archive.Close()
	if err := verifyControlUpdateArchive(archive, update, m.UpdatePublicKey); err != nil {
		return err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	workDir := filepath.Join(m.ControlUpdateWork, update.Version)
	if err := os.MkdirAll(m.ControlUpdateWork, 0o700); err != nil {
		return fmt.Errorf("创建 Control 更新事务根目录失败: %w", err)
	}
	if err := os.Mkdir(workDir, 0o700); err != nil {
		return fmt.Errorf("创建 Control 更新事务目录失败: %w", err)
	}
	keepWork := false
	defer func() {
		if !keepWork {
			_ = os.RemoveAll(workDir)
		}
	}()
	stagedControl, stagedHelper, err := extractControlUpdateArchive(archive, workDir)
	if err != nil {
		return err
	}
	if err := m.validateStagedControlBinaries(ctx, stagedControl, stagedHelper, update.Version); err != nil {
		return err
	}
	transaction := controlUpdateTransaction{
		Version: update.Version, ControlTarget: m.ControlBinary, HelperTarget: m.ConfigHelperBinary,
		ControlBackup: filepath.Join(workDir, "homestack-control.backup"), HelperBackup: filepath.Join(workDir, "homestack-config-helper.backup"),
		ControlUpdateFolder: filepath.Dir(update.ArchivePath),
	}
	if err := copyRegularFile(m.ControlBinary, transaction.ControlBackup, 0o700); err != nil {
		return fmt.Errorf("备份 Control 失败: %w", err)
	}
	if err := copyRegularFile(m.ConfigHelperBinary, transaction.HelperBackup, 0o700); err != nil {
		return fmt.Errorf("备份 Config Helper 失败: %w", err)
	}
	transactionPath := filepath.Join(workDir, "transaction.json")
	if err := atomicJSON(transactionPath, transaction, 0o600); err != nil {
		return err
	}
	if err := installRegularFile(stagedControl, m.ControlBinary); err != nil {
		return fmt.Errorf("安装 Control 失败: %w", err)
	}
	if err := installRegularFile(stagedHelper, m.ConfigHelperBinary); err != nil {
		_ = installRegularFile(transaction.ControlBackup, m.ControlBinary)
		return fmt.Errorf("安装 Config Helper 失败: %w", err)
	}
	arguments := []string{
		"--collect", "--unit=homestack-control-update", "--on-active=2s", "--property=Type=oneshot",
		m.ConfigHelperBinary, "finalize-update", "--transaction=" + transactionPath,
	}
	if _, err := m.Command(ctx, "systemd-run", arguments...); err != nil {
		_ = installRegularFile(transaction.ControlBackup, m.ControlBinary)
		_ = installRegularFile(transaction.HelperBackup, m.ConfigHelperBinary)
		return fmt.Errorf("启动 Control 更新事务失败: %w", err)
	}
	keepWork = true
	return nil
}

func (m *Manager) FinalizeControlUpdate(ctx context.Context, transactionPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	transaction, err := m.readControlUpdateTransaction(transactionPath)
	if err != nil {
		return err
	}
	healthCheck := m.HealthCheck
	if healthCheck == nil {
		healthCheck = m.waitForHealth
	}
	if _, err = m.Command(ctx, "systemctl", "restart", "homestack-control.service"); err == nil {
		err = healthCheck(ctx)
	}
	if err == nil {
		_, err = m.Command(ctx, "systemctl", "restart", "homestack-config-helper.service")
	}
	if err == nil {
		_, err = m.Command(ctx, "systemctl", "restart", "homestack-control.service")
	}
	if err == nil {
		err = healthCheck(ctx)
	}
	if err == nil {
		if cleanupErr := cleanupControlUpdateTransaction(transactionPath, transaction.ControlUpdateFolder); cleanupErr != nil {
			return cleanupErr
		}
		return nil
	}
	failure := err
	rollbackComplete := true
	if restoreErr := installRegularFile(transaction.ControlBackup, transaction.ControlTarget); restoreErr != nil {
		failure = fmt.Errorf("%v；恢复 Control 失败: %w", failure, restoreErr)
		rollbackComplete = false
	}
	if restoreErr := installRegularFile(transaction.HelperBackup, transaction.HelperTarget); restoreErr != nil {
		failure = fmt.Errorf("%v；恢复 Config Helper 失败: %w", failure, restoreErr)
		rollbackComplete = false
	}
	if _, restartErr := m.Command(ctx, "systemctl", "restart", "homestack-config-helper.service"); restartErr != nil {
		failure = fmt.Errorf("%v；恢复后重启 Config Helper 失败: %w", failure, restartErr)
		rollbackComplete = false
	}
	if _, restartErr := m.Command(ctx, "systemctl", "restart", "homestack-control.service"); restartErr != nil {
		failure = fmt.Errorf("%v；恢复后重启 Control 失败: %w", failure, restartErr)
		rollbackComplete = false
	}
	if !rollbackComplete {
		return fmt.Errorf("Control 更新失败且回滚不完整: %w", failure)
	}
	if cleanupErr := cleanupControlUpdateTransaction(transactionPath, transaction.ControlUpdateFolder); cleanupErr != nil {
		return fmt.Errorf("Control 更新失败，已回滚但清理事务失败: %v；%w", failure, cleanupErr)
	}
	return fmt.Errorf("Control 更新失败并已回滚: %w", failure)
}

func cleanupControlUpdateTransaction(transactionPath, updateFolder string) error {
	if err := os.RemoveAll(filepath.Dir(transactionPath)); err != nil {
		return fmt.Errorf("清理 Control 更新事务失败: %w", err)
	}
	if err := os.RemoveAll(updateFolder); err != nil {
		return fmt.Errorf("清理 Control 更新暂存资产失败: %w", err)
	}
	return nil
}

func (m *Manager) validateControlUpdateRequest(update setupapi.ControlUpdateInstallation) error {
	if m.Platform != "linux" || m.Architecture != "amd64" && m.Architecture != "arm64" {
		return errors.New("Control 更新 Helper 只支持 Linux amd64/arm64")
	}
	if !controlUpdateVersionPattern.MatchString(update.Version) {
		return errors.New("Control 更新版本无效")
	}
	expectedFilename := "homestack-control-update_" + update.Version + "_linux_" + m.Architecture + ".tar.gz"
	expectedPath := filepath.Join(m.ControlUpdateRoot, update.Version, expectedFilename)
	if update.Filename != expectedFilename || filepath.Clean(update.ArchivePath) != expectedPath {
		return errors.New("Control 更新暂存路径不在受控目录")
	}
	if update.Size <= 0 || update.Size > 512<<20 || update.Digest == "" || update.Signature == "" {
		return errors.New("Control 更新大小、摘要或签名无效")
	}
	return nil
}

func verifyControlUpdateArchive(file *os.File, update setupapi.ControlUpdateInstallation, publicKeyEncoded string) error {
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(file, update.Size+1))
	if err != nil || written != update.Size {
		return errors.New("Control 更新资产大小校验失败")
	}
	actual := digest.Sum(nil)
	expected, err := decodeUpdateDigest(update.Digest)
	if err != nil || len(expected) != sha256.Size || !strings.EqualFold(hex.EncodeToString(actual), hex.EncodeToString(expected)) {
		return errors.New("Control 更新 SHA-256 校验失败")
	}
	publicKey, keyErr := decodeUpdateBase64(publicKeyEncoded)
	signature, signatureErr := decodeUpdateBase64(update.Signature)
	if keyErr != nil || len(publicKey) != ed25519.PublicKeySize || signatureErr != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), actual, signature) {
		return errors.New("Control 更新 Ed25519 签名校验失败")
	}
	return nil
}

func extractControlUpdateArchive(file *os.File, directory string) (string, string, error) {
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return "", "", fmt.Errorf("打开 Control 更新 gzip 失败: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	expected := map[string]string{
		"homestack-control":       filepath.Join(directory, "homestack-control.new"),
		"homestack-config-helper": filepath.Join(directory, "homestack-config-helper.new"),
	}
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("读取 Control 更新归档失败: %w", err)
		}
		destination, ok := expected[header.Name]
		if !ok || seen[header.Name] || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 512<<20 {
			return "", "", fmt.Errorf("Control 更新归档包含非法条目: %s", header.Name)
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return "", "", err
		}
		written, copyErr := io.Copy(output, io.LimitReader(reader, header.Size+1))
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return "", "", errors.New("解包 Control 更新程序失败或大小不匹配")
		}
		seen[header.Name] = true
	}
	if len(seen) != len(expected) {
		return "", "", errors.New("Control 更新归档必须且只能包含 Control 与 Config Helper")
	}
	return expected["homestack-control"], expected["homestack-config-helper"], nil
}

func (m *Manager) validateStagedControlBinaries(ctx context.Context, controlPath, helperPath, version string) error {
	for _, expected := range []struct{ path, name string }{{controlPath, "homestack-control"}, {helperPath, "homestack-config-helper"}} {
		commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		output, err := m.RunVersion(commandContext, expected.path)
		cancel()
		if err != nil {
			return fmt.Errorf("执行暂存 %s 失败: %w", expected.name, err)
		}
		var metadata struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			GOOS    string `json:"goos"`
			GOARCH  string `json:"goarch"`
		}
		if err := json.Unmarshal(output, &metadata); err != nil || metadata.Name != expected.name || strings.TrimPrefix(metadata.Version, "v") != version || metadata.GOOS != m.Platform || metadata.GOARCH != m.Architecture {
			return fmt.Errorf("暂存 %s 版本或平台不匹配", expected.name)
		}
	}
	return nil
}

func (m *Manager) readControlUpdateTransaction(path string) (controlUpdateTransaction, error) {
	var transaction controlUpdateTransaction
	data, err := os.ReadFile(path)
	if err != nil {
		return transaction, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&transaction); err != nil {
		return transaction, err
	}
	expected := filepath.Join(m.ControlUpdateWork, transaction.Version, "transaction.json")
	if !controlUpdateVersionPattern.MatchString(transaction.Version) || filepath.Clean(path) != expected || transaction.ControlTarget != m.ControlBinary || transaction.HelperTarget != m.ConfigHelperBinary || transaction.ControlBackup != filepath.Join(filepath.Dir(expected), "homestack-control.backup") || transaction.HelperBackup != filepath.Join(filepath.Dir(expected), "homestack-config-helper.backup") {
		return transaction, errors.New("Control 更新事务路径或目标无效")
	}
	return transaction, nil
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("源程序不是常规文件")
	}
	return atomicWrite(destination, data, mode)
}

func installRegularFile(source, destination string) error {
	return copyRegularFile(source, destination, 0o755)
}

func decodeUpdateDigest(raw string) ([]byte, error) {
	if value, err := hex.DecodeString(raw); err == nil {
		return value, nil
	}
	return decodeUpdateBase64(raw)
}

func decodeUpdateBase64(raw string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if value, err := encoding.DecodeString(strings.TrimSpace(raw)); err == nil {
			return value, nil
		}
	}
	return nil, errors.New("base64 编码无效")
}

func isProductionControlUpdateTransaction(path string) bool {
	clean := filepath.Clean(path)
	relative, err := filepath.Rel("/var/lib/homestack-setup/control-updates", clean)
	return err == nil && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && filepath.Base(clean) == "transaction.json"
}
