//go:build linux

package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func localMetrics(ctx context.Context) (SystemMetrics, error) {
	memoryUsed, memoryTotal, err := readLinuxMemory()
	if err != nil {
		return SystemMetrics{}, err
	}
	var disk syscall.Statfs_t
	if err := syscall.Statfs("/", &disk); err != nil {
		return SystemMetrics{}, fmt.Errorf("读取 Linux 磁盘指标失败: %w", err)
	}
	first, err := readLinuxCPU()
	if err != nil {
		return SystemMetrics{}, err
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return SystemMetrics{}, ctx.Err()
	case <-timer.C:
	}
	second, err := readLinuxCPU()
	if err != nil {
		return SystemMetrics{}, err
	}
	totalDelta, idleDelta := second.total-first.total, second.idle-first.idle
	if totalDelta == 0 || idleDelta > totalDelta {
		return SystemMetrics{}, errors.New("Linux CPU 采样结果无效")
	}
	diskTotal := disk.Blocks * uint64(disk.Bsize)
	diskFree := disk.Bavail * uint64(disk.Bsize)
	return SystemMetrics{
		CPUPercent:  100 * (1 - float64(idleDelta)/float64(totalDelta)),
		MemoryUsed:  memoryUsed,
		MemoryTotal: memoryTotal,
		DiskUsed:    diskTotal - diskFree,
		DiskTotal:   diskTotal,
	}, nil
}

func readLinuxMemory() (uint64, uint64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("读取 Linux 内存指标失败: %w", err)
	}
	defer file.Close()
	values := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:") {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("解析 Linux 内存指标失败: %w", parseErr)
		}
		values[fields[0]] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("读取 Linux 内存指标失败: %w", err)
	}
	total, available := values["MemTotal:"], values["MemAvailable:"]
	if total == 0 || available > total {
		return 0, 0, errors.New("Linux 内存指标不完整")
	}
	return total - available, total, nil
}

type linuxCPUTimes struct{ total, idle uint64 }

func readLinuxCPU() (linuxCPUTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return linuxCPUTimes{}, fmt.Errorf("读取 Linux CPU 指标失败: %w", err)
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return linuxCPUTimes{}, errors.New("Linux CPU 指标不完整")
	}
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, parseErr := strconv.ParseUint(field, 10, 64)
		if parseErr != nil {
			return linuxCPUTimes{}, fmt.Errorf("解析 Linux CPU 指标失败: %w", parseErr)
		}
		values = append(values, value)
	}
	result := linuxCPUTimes{idle: values[3]}
	if len(values) > 4 {
		result.idle += values[4]
	}
	for _, value := range values {
		result.total += value
	}
	return result, nil
}
