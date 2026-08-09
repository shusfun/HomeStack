//go:build darwin

package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func localMetrics(ctx context.Context) (SystemMetrics, error) {
	total, err := commandUint(ctx, "sysctl", "-n", "hw.memsize")
	if err != nil {
		return SystemMetrics{}, err
	}
	vmOutput, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return SystemMetrics{}, fmt.Errorf("读取 macOS 内存指标失败: %w", err)
	}
	used, err := parseVMStat(string(vmOutput))
	if err != nil {
		return SystemMetrics{}, err
	}
	diskOutput, err := exec.CommandContext(ctx, "df", "-k", "/").Output()
	if err != nil {
		return SystemMetrics{}, fmt.Errorf("读取 macOS 磁盘指标失败: %w", err)
	}
	diskUsed, diskTotal, err := parseDF(string(diskOutput))
	if err != nil {
		return SystemMetrics{}, err
	}
	psOutput, err := exec.CommandContext(ctx, "ps", "-A", "-o", "%cpu=").Output()
	if err != nil {
		return SystemMetrics{}, fmt.Errorf("读取 macOS CPU 指标失败: %w", err)
	}
	cpu := 0.0
	for _, field := range strings.Fields(string(psOutput)) {
		value, parseErr := strconv.ParseFloat(strings.ReplaceAll(field, ",", "."), 64)
		if parseErr != nil {
			return SystemMetrics{}, fmt.Errorf("解析 macOS CPU 指标失败: %w", parseErr)
		}
		cpu += value
	}
	cpu /= float64(runtime.NumCPU())
	if cpu > 100 {
		cpu = 100
	}
	return SystemMetrics{CPUPercent: cpu, MemoryUsed: used, MemoryTotal: total, DiskUsed: diskUsed, DiskTotal: diskTotal}, nil
}

func commandUint(ctx context.Context, name string, args ...string) (uint64, error) {
	output, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func parseVMStat(output string) (uint64, error) {
	pageSize := uint64(4096)
	usedPages := uint64(0)
	found := false
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "page size of") {
			fields := strings.Fields(line)
			for index, field := range fields {
				if field == "of" && index+1 < len(fields) {
					pageSize, _ = strconv.ParseUint(fields[index+1], 10, 64)
				}
			}
		}
		if !strings.HasPrefix(line, "Pages active:") && !strings.HasPrefix(line, "Pages wired down:") && !strings.HasPrefix(line, "Pages occupied by compressor:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimSuffix(strings.SplitN(line, ":", 2)[1], "."))
		pages, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("解析 macOS 内存页失败: %w", err)
		}
		usedPages += pages
		found = true
	}
	if !found {
		return 0, errors.New("macOS vm_stat 缺少内存页数据")
	}
	return usedPages * pageSize, nil
}

func parseDF(output string) (uint64, uint64, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return 0, 0, errors.New("macOS df 输出不完整")
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 5 {
		return 0, 0, errors.New("macOS df 字段不完整")
	}
	total, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	used, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return used * 1024, total * 1024, nil
}
