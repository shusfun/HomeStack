//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	globalMemoryStatusExProc = kernel32.NewProc("GlobalMemoryStatusEx")
	getSystemTimesProc       = kernel32.NewProc("GetSystemTimes")
)

type windowsMemoryStatus struct {
	Length, MemoryLoad                           uint32
	TotalPhysical, AvailablePhysical             uint64
	TotalPageFile, AvailablePageFile             uint64
	TotalVirtual, AvailableVirtual, AvailableExt uint64
}

type windowsCPUTimes struct {
	idle, kernel, user uint64
}

func localMetrics(ctx context.Context) (SystemMetrics, error) {
	memory := windowsMemoryStatus{Length: uint32(unsafe.Sizeof(windowsMemoryStatus{}))}
	if result, _, callErr := globalMemoryStatusExProc.Call(uintptr(unsafe.Pointer(&memory))); result == 0 {
		return SystemMetrics{}, fmt.Errorf("读取 Windows 内存指标失败: %w", callErr)
	}
	volume := strings.TrimSpace(os.Getenv("SystemDrive"))
	if len(volume) != 2 || volume[1] != ':' {
		return SystemMetrics{}, errors.New("Windows SystemDrive 环境变量无效")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return SystemMetrics{}, fmt.Errorf("解析 Windows 系统盘失败: %w", err)
	}
	var freeAvailable, totalDisk, freeDisk uint64
	if err := windows.GetDiskFreeSpaceEx(root, &freeAvailable, &totalDisk, &freeDisk); err != nil {
		return SystemMetrics{}, fmt.Errorf("读取 Windows 磁盘指标失败: %w", err)
	}
	first, err := readWindowsCPUTimes()
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
	second, err := readWindowsCPUTimes()
	if err != nil {
		return SystemMetrics{}, err
	}
	totalDelta := (second.kernel - first.kernel) + (second.user - first.user)
	idleDelta := second.idle - first.idle
	if totalDelta == 0 || idleDelta > totalDelta {
		return SystemMetrics{}, errors.New("Windows CPU 采样结果无效")
	}
	return SystemMetrics{
		CPUPercent:  100 * (1 - float64(idleDelta)/float64(totalDelta)),
		MemoryUsed:  memory.TotalPhysical - memory.AvailablePhysical,
		MemoryTotal: memory.TotalPhysical,
		DiskUsed:    totalDisk - freeDisk,
		DiskTotal:   totalDisk,
	}, nil
}

func readWindowsCPUTimes() (windowsCPUTimes, error) {
	var idle, kernel, user windows.Filetime
	if result, _, callErr := getSystemTimesProc.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); result == 0 {
		return windowsCPUTimes{}, fmt.Errorf("读取 Windows CPU 指标失败: %w", callErr)
	}
	return windowsCPUTimes{idle: filetimeUint64(idle), kernel: filetimeUint64(kernel), user: filetimeUint64(user)}, nil
}

func filetimeUint64(value windows.Filetime) uint64 {
	return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
}
