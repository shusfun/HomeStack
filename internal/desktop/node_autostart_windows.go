//go:build windows

package desktop

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	windowsRunKeyPath         = `Software\Microsoft\Windows\CurrentVersion\Run`
	windowsRunValueName       = "HomeStackNode"
	windowsNodePort           = 19444
	tcpTableOwnerPIDListen    = 3
	windowsProcessSynchronize = 0x00100000
	windowsNodeMutexName      = `Local\HomeStackNode`
)

var getExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

func AcquireNodeInstance() (func(), error) {
	name, err := windows.UTF16PtrFromString(windowsNodeMutexName)
	if err != nil {
		return nil, fmt.Errorf("生成 HomeStack Node 互斥锁名称失败: %w", err)
	}
	mutex, err := windows.CreateMutex(nil, true, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if mutex != 0 {
			_ = windows.CloseHandle(mutex)
		}
		return nil, errors.New("HomeStack Node 已在当前用户会话中运行")
	}
	if err != nil {
		return nil, fmt.Errorf("创建 HomeStack Node 互斥锁失败: %w", err)
	}
	return func() {
		_ = windows.ReleaseMutex(mutex)
		_ = windows.CloseHandle(mutex)
	}, nil
}

func configureWindowsStartup(executable string) error {
	command, err := windowsNodeCommand(executable)
	if err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		key, _, err = registry.CreateKey(registry.CURRENT_USER, windowsRunKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	}
	if err != nil {
		return fmt.Errorf("打开当前用户 HomeStack Node 自启动注册表失败: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue(windowsRunValueName, command); err != nil {
		return fmt.Errorf("写入当前用户 HomeStack Node 自启动注册表失败: %w", err)
	}
	stored, _, err := key.GetStringValue(windowsRunValueName)
	if err != nil {
		return fmt.Errorf("校验当前用户 HomeStack Node 自启动注册表失败: %w", err)
	}
	if stored != command {
		return errors.New("当前用户 HomeStack Node 自启动注册表写入后内容不一致")
	}
	return nil
}

func windowsNodeCommand(executable string) (string, error) {
	executable = filepath.Clean(strings.TrimSpace(executable))
	if !filepath.IsAbs(executable) {
		return "", errors.New("HomeStack App 路径必须是绝对路径")
	}
	if strings.ContainsRune(executable, '"') {
		return "", errors.New("HomeStack App 路径不能包含双引号")
	}
	return `"` + executable + `" --node`, nil
}

func restartWindowsNode(executable string) error {
	pids, err := windowsNodeListenerPIDs()
	if err != nil {
		return err
	}
	for _, pid := range pids {
		if pid == uint32(os.Getpid()) {
			return errors.New("当前桌面进程异常占用了 HomeStack Node 端口 19444")
		}
		if err := terminateWindowsNode(pid, executable); err != nil {
			return err
		}
	}
	if len(pids) > 0 {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			remaining, err := windowsNodeListenerPIDs()
			if err != nil {
				return err
			}
			if len(remaining) == 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		remaining, err := windowsNodeListenerPIDs()
		if err != nil {
			return err
		}
		if len(remaining) > 0 {
			return fmt.Errorf("旧 HomeStack Node 未在限定时间内释放端口 19444，PID=%v", remaining)
		}
	}
	return startWindowsNode(executable)
}

func terminateWindowsNode(pid uint32, expectedExecutable string) error {
	access := uint32(windows.PROCESS_QUERY_LIMITED_INFORMATION | windowsProcessSynchronize)
	process, err := windows.OpenProcess(access, false, pid)
	if err != nil {
		return fmt.Errorf("打开占用端口 19444 的进程失败，PID=%d: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	pathBuffer := make([]uint16, 32768)
	pathLength := uint32(len(pathBuffer))
	if err := windows.QueryFullProcessImageName(process, 0, &pathBuffer[0], &pathLength); err != nil {
		return fmt.Errorf("读取占用端口 19444 的进程路径失败，PID=%d: %w", pid, err)
	}
	actualExecutable := filepath.Clean(windows.UTF16ToString(pathBuffer[:pathLength]))
	if !isManagedWindowsNodeExecutable(expectedExecutable, actualExecutable) {
		return fmt.Errorf("端口 19444 被其他程序占用，PID=%d，路径=%s", pid, actualExecutable)
	}
	descendants, err := windowsProcessDescendants(pid)
	if err != nil {
		return err
	}
	for index := len(descendants) - 1; index >= 0; index-- {
		if err := terminateWindowsProcess(descendants[index]); err != nil {
			return fmt.Errorf("停止旧 HomeStack Node 子进程失败，PID=%d: %w", descendants[index], err)
		}
	}
	if err := terminateWindowsProcess(pid); err != nil {
		return fmt.Errorf("停止旧 HomeStack Node 失败，PID=%d: %w", pid, err)
	}
	return nil
}

func isManagedWindowsNodeExecutable(expectedExecutable, actualExecutable string) bool {
	expected := filepath.Clean(strings.TrimSpace(expectedExecutable))
	actual := filepath.Clean(strings.TrimSpace(actualExecutable))
	if strings.EqualFold(actual, expected) {
		return true
	}
	if !strings.EqualFold(filepath.Dir(actual), filepath.Dir(expected)) {
		return false
	}
	expectedName := filepath.Base(expected)
	actualName := filepath.Base(actual)
	prefix := expectedName + ".old."
	if len(actualName) <= len(prefix) || !strings.EqualFold(actualName[:len(prefix)], prefix) {
		return false
	}
	for _, character := range actualName[len(prefix):] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func windowsProcessDescendants(root uint32) ([]uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("枚举旧 HomeStack Node 子进程失败: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	children := make(map[uint32][]uint32)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("读取 Windows 进程表失败: %w", err)
	}
	for {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("继续读取 Windows 进程表失败: %w", err)
		}
	}
	descendants := make([]uint32, 0)
	queue := append([]uint32(nil), children[root]...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		descendants = append(descendants, pid)
		queue = append(queue, children[pid]...)
	}
	return descendants, nil
}

func terminateWindowsProcess(pid uint32) error {
	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windowsProcessSynchronize, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.TerminateProcess(process, 0); err != nil {
		return err
	}
	event, err := windows.WaitForSingleObject(process, 10_000)
	if err != nil {
		return err
	}
	if event != windows.WAIT_OBJECT_0 {
		return errors.New("进程未在限定时间内退出")
	}
	return nil
}

func startWindowsNode(executable string) error {
	stateDir, err := nodeStateDirectory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("创建 Node 状态目录失败: %w", err)
	}
	stdout, err := os.OpenFile(filepath.Join(stateDir, "node.stdout.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开 Node 标准输出日志失败: %w", err)
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(filepath.Join(stateDir, "node.stderr.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开 Node 错误日志失败: %w", err)
	}
	defer stderr.Close()
	command := exec.Command(executable, "--node")
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动 HomeStack Node 失败: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("释放 HomeStack Node 进程句柄失败: %w", err)
	}
	return nil
}

func windowsNodeListenerPIDs() ([]uint32, error) {
	var size uint32
	result, _, _ := getExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, tcpTableOwnerPIDListen, 0)
	if syscall.Errno(result) != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, fmt.Errorf("查询 HomeStack Node 端口表大小失败: %w", syscall.Errno(result))
	}
	buffer := make([]byte, size)
	result, _, _ = getExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, tcpTableOwnerPIDListen, 0)
	if result != 0 {
		return nil, fmt.Errorf("查询 HomeStack Node 端口表失败: %w", syscall.Errno(result))
	}
	if len(buffer) < 4 {
		return nil, errors.New("Windows TCP 端口表响应不完整")
	}
	count := int(binary.LittleEndian.Uint32(buffer[:4]))
	const rowSize = 24
	if count < 0 || 4+count*rowSize > len(buffer) {
		return nil, errors.New("Windows TCP 端口表行数无效")
	}
	pids := make([]uint32, 0, 1)
	for index := 0; index < count; index++ {
		row := buffer[4+index*rowSize : 4+(index+1)*rowSize]
		if binary.BigEndian.Uint16(row[8:10]) == windowsNodePort {
			pids = append(pids, binary.LittleEndian.Uint32(row[20:24]))
		}
	}
	return pids, nil
}
