//go:build linux

package desktop

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func restartAppImage(app *application.App, downloadedPath string) error {
	if app == nil {
		return errors.New("Wails App 尚未初始化")
	}
	target := filepath.Clean(os.Getenv("APPIMAGE"))
	if err := validateAppImageFile(target, "当前 AppImage"); err != nil {
		return err
	}
	if err := validateAppImageFile(downloadedPath, "暂存 AppImage"); err != nil {
		return err
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("读取当前 AppImage 失败: %w", err)
	}
	prepared, err := copyAppImageBesideTarget(downloadedPath, target, targetInfo.Mode().Perm())
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		_ = os.Remove(prepared)
		return fmt.Errorf("定位 AppImage helper 失败: %w", err)
	}
	command := exec.Command(self, "--appimage-update-helper",
		"--parent-pid="+strconv.Itoa(os.Getpid()), "--target="+target, "--staged="+prepared)
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = os.Remove(prepared)
		return fmt.Errorf("启动 AppImage 更新 helper 失败: %w", err)
	}
	if stagingDir := filepath.Dir(downloadedPath); strings.HasPrefix(filepath.Base(stagingDir), "wails-update-") {
		_ = os.RemoveAll(stagingDir)
	}
	app.Quit()
	return nil
}

func RunAppImageUpdateHelper(arguments []string) error {
	flags := flag.NewFlagSet("appimage-update-helper", flag.ContinueOnError)
	parentPID := flags.Int("parent-pid", 0, "待退出的桌面进程")
	target := flags.String("target", "", "当前 AppImage")
	staged := flags.String("staged", "", "同目录暂存 AppImage")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *parentPID <= 1 {
		return errors.New("AppImage 更新 parent PID 无效")
	}
	if err := validateAppImageFile(*target, "当前 AppImage"); err != nil {
		return err
	}
	if err := validateAppImageFile(*staged, "暂存 AppImage"); err != nil {
		return err
	}
	if filepath.Dir(filepath.Clean(*target)) != filepath.Dir(filepath.Clean(*staged)) || !strings.HasPrefix(filepath.Base(*staged), ".homestack-appimage-update-") {
		return errors.New("暂存 AppImage 不在目标文件同级受控路径")
	}
	if err := waitDesktopExit(*parentPID, 30*time.Second); err != nil {
		return err
	}
	backup := *target + ".bak"
	if _, err := os.Lstat(backup); err == nil {
		return fmt.Errorf("AppImage 更新备份已存在: %s", backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 AppImage 备份失败: %w", err)
	}
	if err := os.Rename(*target, backup); err != nil {
		return fmt.Errorf("备份旧 AppImage 失败: %w", err)
	}
	if err := os.Rename(*staged, *target); err != nil {
		_ = os.Rename(backup, *target)
		return fmt.Errorf("替换 AppImage 失败: %w", err)
	}
	if err := startDetachedAppImage(*target); err != nil {
		failed := *target + ".failed"
		_ = os.Rename(*target, failed)
		if restoreErr := os.Rename(backup, *target); restoreErr != nil {
			return fmt.Errorf("启动新 AppImage 失败: %v；恢复备份也失败: %w", err, restoreErr)
		}
		if restoreErr := startDetachedAppImage(*target); restoreErr != nil {
			return fmt.Errorf("启动新 AppImage 失败: %v；旧版本已恢复但启动失败: %w", err, restoreErr)
		}
		return fmt.Errorf("启动新 AppImage 失败: %v；已恢复旧版本，失败文件保留在 %s", err, failed)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("清理 AppImage 备份失败: %w", err)
	}
	return nil
}

func validateAppImageFile(path, label string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s 必须是绝对路径", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("读取%s失败: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s必须是常规文件", label)
	}
	return nil
}

func copyAppImageBesideTarget(source, target string, mode os.FileMode) (string, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("打开暂存 AppImage 失败: %w", err)
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(target), ".homestack-appimage-update-")
	if err != nil {
		return "", fmt.Errorf("在 AppImage 同文件系统创建暂存文件失败: %w", err)
	}
	path := output.Name()
	cleanup := func() {
		_ = output.Close()
		_ = os.Remove(path)
	}
	if err := output.Chmod(mode); err != nil {
		cleanup()
		return "", fmt.Errorf("设置暂存 AppImage 权限失败: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		cleanup()
		return "", fmt.Errorf("复制暂存 AppImage 失败: %w", err)
	}
	if err := output.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("同步暂存 AppImage 失败: %w", err)
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("关闭暂存 AppImage 失败: %w", err)
	}
	return path, nil
}

func waitDesktopExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("等待桌面进程退出超时")
}

func startDetachedAppImage(path string) error {
	command := exec.Command(path)
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return command.Start()
}
