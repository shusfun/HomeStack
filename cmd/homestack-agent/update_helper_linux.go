//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func runUpdateHelper(arguments []string) error {
	flags := flag.NewFlagSet("update-helper", flag.ContinueOnError)
	parentPID := flags.Int("parent-pid", 0, "待停止的 Agent PID")
	target := flags.String("target", "", "Agent 当前可执行文件")
	staged := flags.String("staged", "", "已校验的暂存程序")
	backup := flags.String("backup", "", "旧版本备份路径")
	healthURL := flags.String("health-url", "", "Agent HTTPS 健康检查地址")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := validateUpdateHelperPaths(*target, *staged, *backup); err != nil {
		return err
	}
	parsedHealth, err := url.Parse(*healthURL)
	if err != nil || parsedHealth.Scheme != "https" || parsedHealth.Hostname() == "" || parsedHealth.Path != "/api/v1/health" {
		return errors.New("Agent 更新健康检查地址无效")
	}
	if *parentPID <= 1 {
		return errors.New("Agent 更新 parent PID 无效")
	}
	time.Sleep(time.Second)
	if output, err := exec.Command("systemctl", "--user", "stop", "homestack-agent.service").CombinedOutput(); err != nil {
		return fmt.Errorf("停止 Agent 服务失败: %s", strings.TrimSpace(string(output)))
	}
	if err := waitProcessExit(*parentPID, 30*time.Second); err != nil {
		return restartOldAgent(*healthURL, err)
	}
	if err := os.Rename(*target, *backup); err != nil {
		return restartOldAgent(*healthURL, fmt.Errorf("备份 Agent 旧版本失败: %w", err))
	}
	if err := os.Rename(*staged, *target); err != nil {
		cause := fmt.Errorf("替换 Agent 新版本失败: %w", err)
		if restoreErr := os.Rename(*backup, *target); restoreErr != nil {
			return fmt.Errorf("%v；且恢复 Agent 备份时出错: %w", cause, restoreErr)
		}
		return restartOldAgent(*healthURL, cause)
	}
	if err := os.Chmod(*target, 0o755); err != nil {
		return rollbackAgent(*target, *backup, *staged+".failed", *healthURL, fmt.Errorf("设置 Agent 新版本权限失败: %w", err))
	}
	if output, err := exec.Command("systemctl", "--user", "start", "homestack-agent.service").CombinedOutput(); err != nil {
		return rollbackAgent(*target, *backup, *staged+".failed", *healthURL, fmt.Errorf("启动 Agent 新版本失败: %s", strings.TrimSpace(string(output))))
	}
	if err := waitAgentHealth(*healthURL, 45*time.Second); err != nil {
		_, _ = exec.Command("systemctl", "--user", "stop", "homestack-agent.service").CombinedOutput()
		return rollbackAgent(*target, *backup, *staged+".failed", *healthURL, err)
	}
	if err := os.Remove(*backup); err != nil {
		return fmt.Errorf("清理 Agent 旧版本备份失败: %w", err)
	}
	if err := os.RemoveAll(filepath.Dir(*staged)); err != nil {
		return fmt.Errorf("清理 Agent 更新暂存目录失败: %w", err)
	}
	return nil
}

func validateUpdateHelperPaths(target, staged, backup string) error {
	for _, path := range []string{target, staged, backup} {
		if !filepath.IsAbs(path) {
			return errors.New("Agent 更新路径必须是绝对路径")
		}
	}
	target = filepath.Clean(target)
	staged = filepath.Clean(staged)
	backup = filepath.Clean(backup)
	if backup == target || !strings.HasPrefix(backup, target+".backup-") {
		return errors.New("Agent 更新备份路径不属于目标程序")
	}
	stagingDir := filepath.Dir(staged)
	if filepath.Dir(stagingDir) != filepath.Dir(target) || !strings.HasPrefix(filepath.Base(stagingDir), ".homestack-agent-update-") || filepath.Base(staged) != "homestack-agent" {
		return errors.New("Agent 更新暂存路径不在目标程序同级受控目录")
	}
	for _, path := range []string{target, staged} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Agent 更新路径不是常规文件: %s", path)
		}
	}
	return nil
}

func waitProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待 Agent 进程 %s 退出超时", strconv.Itoa(pid))
}

func waitAgentHealth(endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		response, err := client.Do(request)
		cancel()
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return errors.New("Agent 健康检查超时")
}

func rollbackAgent(target, backup, failed, healthURL string, cause error) error {
	if err := os.Rename(target, failed); err != nil {
		return fmt.Errorf("%v；且保留失败版本时出错: %w", cause, err)
	}
	if err := os.Rename(backup, target); err != nil {
		return fmt.Errorf("%v；且恢复 Agent 备份时出错: %w", cause, err)
	}
	return restartOldAgent(healthURL, fmt.Errorf("%v；失败版本保留在 %s", cause, failed))
}

func restartOldAgent(healthURL string, cause error) error {
	output, err := exec.Command("systemctl", "--user", "start", "homestack-agent.service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v；旧版本已就位但重启失败: %s", cause, strings.TrimSpace(string(output)))
	}
	if err := waitAgentHealth(healthURL, 45*time.Second); err != nil {
		return fmt.Errorf("%v；旧版本已就位并重启，但健康检查也失败: %w", cause, err)
	}
	return fmt.Errorf("%v；已自动恢复旧版本并通过健康检查", cause)
}
