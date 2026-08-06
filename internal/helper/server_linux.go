//go:build linux

package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wangshangbin/homestack/internal/agent"
)

type serviceSpec struct {
	Unit    string
	User    bool
	Actions []string
}

var serviceSpecs = map[string]serviceSpec{
	"tailscale":       {Unit: "tailscaled.service", Actions: []string{"restart"}},
	"homestack-agent": {Unit: "homestack-agent.service", User: true, Actions: []string{"restart"}},
	"filebrowser":     {Unit: "filebrowser.service", Actions: []string{"start", "stop", "restart"}},
	"jellyfin":        {Unit: "jellyfin.service", Actions: []string{"start", "stop", "restart"}},
}

var secretPattern = regexp.MustCompile(`(?i)(authorization|token|secret|password|cookie)(\s*[:=]\s*)\S+`)

type Server struct {
	SocketPath string
	AllowedUID uint32
	username   string
}

func Run(ctx context.Context, socketPath string, allowedUID uint32) error {
	if os.Geteuid() != 0 {
		return errors.New("homestack-helper 必须由 root 启动")
	}
	if socketPath == "" || allowedUID == 0 {
		return errors.New("helper 必须明确配置 Unix Socket 和非 root 调用 UID")
	}
	account, err := user.LookupId(strconv.FormatUint(uint64(allowedUID), 10))
	if err != nil {
		return fmt.Errorf("查找 Agent 用户失败: %w", err)
	}
	if err := os.MkdirAll("/run/homestack", 0o755); err != nil {
		return fmt.Errorf("创建 helper 运行目录失败: %w", err)
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("监听 helper Unix Socket 失败: %w", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if err := os.Chown(socketPath, int(allowedUID), -1); err != nil {
		return fmt.Errorf("设置 helper Socket 所有者失败: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("设置 helper Socket 权限失败: %w", err)
	}
	server := &Server{SocketPath: socketPath, AllowedUID: allowedUID, username: account.Username}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("接受 helper 连接失败: %w", err)
		}
		go server.handle(connection)
	}
}

func (s *Server) handle(connection net.Conn) {
	defer connection.Close()
	response := agent.HelperResponse{}
	if err := s.authorize(connection); err != nil {
		response.Error = err.Error()
		_ = json.NewEncoder(connection).Encode(response)
		return
	}
	_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
	var request agent.HelperRequest
	decoder := json.NewDecoder(io.LimitReader(connection, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.Error = "解析 helper 请求失败: " + err.Error()
	} else {
		response = s.dispatch(request)
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func (s *Server) authorize(connection net.Conn) error {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return errors.New("helper 只接受 Unix Socket")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return fmt.Errorf("读取 peer credential 失败: %w", err)
	}
	var credential *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("读取 peer credential 失败: %w", err)
	}
	if socketErr != nil {
		return fmt.Errorf("读取 peer credential 失败: %w", socketErr)
	}
	if credential == nil || credential.Uid != s.AllowedUID {
		return errors.New("helper 拒绝非 Agent 用户连接")
	}
	return nil
}

func (s *Server) dispatch(request agent.HelperRequest) agent.HelperResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	switch request.Operation {
	case "metrics":
		metrics, err := readMetrics()
		if err != nil {
			return agent.HelperResponse{Error: err.Error()}
		}
		return agent.HelperResponse{Metrics: &metrics}
	case "services":
		services, err := s.services(ctx)
		if err != nil {
			return agent.HelperResponse{Error: err.Error()}
		}
		return agent.HelperResponse{Services: services}
	case "action":
		if err := s.action(ctx, request.Service, request.Action); err != nil {
			return agent.HelperResponse{Error: err.Error()}
		}
		return agent.HelperResponse{}
	case "logs":
		logs, err := s.logs(ctx, request.Service, request.Limit, request.Cursor)
		if err != nil {
			return agent.HelperResponse{Error: err.Error()}
		}
		return agent.HelperResponse{Logs: &logs}
	default:
		return agent.HelperResponse{Error: "helper 操作不在白名单中"}
	}
}

func (s *Server) services(ctx context.Context) ([]agent.ServiceStatus, error) {
	ids := []string{"tailscale", "homestack-agent", "filebrowser", "jellyfin"}
	result := make([]agent.ServiceStatus, 0, len(ids))
	for _, id := range ids {
		spec := serviceSpecs[id]
		output, err := s.systemctl(ctx, spec, "show", spec.Unit, "--property=ActiveState,SubState", "--value")
		if err != nil {
			return nil, fmt.Errorf("读取服务 %s 状态失败: %w", id, err)
		}
		lines := strings.Fields(string(output))
		state := "unknown"
		detail := ""
		if len(lines) > 0 {
			state = lines[0]
		}
		if len(lines) > 1 {
			detail = lines[1]
		}
		result = append(result, agent.ServiceStatus{ID: id, State: state, Detail: detail, Actions: append([]string(nil), spec.Actions...)})
	}
	return result, nil
}

func (s *Server) action(ctx context.Context, id, action string) error {
	spec, ok := serviceSpecs[id]
	if !ok || !contains(spec.Actions, action) {
		return errors.New("服务 ID 或动作不在白名单中")
	}
	if _, err := s.systemctl(ctx, spec, action, spec.Unit); err != nil {
		return fmt.Errorf("执行服务动作失败: %w", err)
	}
	return nil
}

func (s *Server) logs(ctx context.Context, id string, limit int, cursor string) (agent.LogPage, error) {
	spec, ok := serviceSpecs[id]
	if !ok {
		return agent.LogPage{}, errors.New("日志服务 ID 不在白名单中")
	}
	if limit < 1 || limit > 500 {
		return agent.LogPage{}, errors.New("日志行数必须介于 1 和 500")
	}
	if len(cursor) > 1024 || strings.ContainsAny(cursor, "\r\n\t ") {
		return agent.LogPage{}, errors.New("日志游标格式无效")
	}
	arguments := []string{"--no-pager", "--output=json", "--unit=" + spec.Unit, "--lines=" + strconv.Itoa(limit)}
	if spec.User {
		arguments = append([]string{"--user", "--machine=" + s.username + "@.host"}, arguments...)
	}
	if cursor != "" {
		arguments = append(arguments, "--after-cursor="+cursor)
	}
	output, err := exec.CommandContext(ctx, "journalctl", arguments...).Output()
	if err != nil {
		return agent.LogPage{}, fmt.Errorf("读取固定服务日志失败: %w", err)
	}
	page := agent.LogPage{Entries: make([]agent.LogEntry, 0, limit)}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var row struct {
			Cursor    string `json:"__CURSOR"`
			Timestamp string `json:"__REALTIME_TIMESTAMP"`
			Message   any    `json:"MESSAGE"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return agent.LogPage{}, fmt.Errorf("解析 journal 日志失败: %w", err)
		}
		micros, _ := strconv.ParseInt(row.Timestamp, 10, 64)
		message := secretPattern.ReplaceAllString(fmt.Sprint(row.Message), "$1$2[REDACTED]")
		page.Entries = append(page.Entries, agent.LogEntry{Cursor: row.Cursor, Timestamp: time.UnixMicro(micros).UTC(), Service: id, Message: message})
		page.NextCursor = row.Cursor
	}
	if err := scanner.Err(); err != nil {
		return agent.LogPage{}, fmt.Errorf("读取 journal 日志失败: %w", err)
	}
	return page, nil
}

func (s *Server) systemctl(ctx context.Context, spec serviceSpec, arguments ...string) ([]byte, error) {
	args := append([]string(nil), arguments...)
	if spec.User {
		args = append([]string{"--user", "--machine=" + s.username + "@.host"}, args...)
	}
	output, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	return output, nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func readMetrics() (agent.SystemMetrics, error) {
	beforeTotal, beforeIdle, err := readCPU()
	if err != nil {
		return agent.SystemMetrics{}, err
	}
	time.Sleep(200 * time.Millisecond)
	afterTotal, afterIdle, err := readCPU()
	if err != nil {
		return agent.SystemMetrics{}, err
	}
	memoryUsed, memoryTotal, err := readMemory()
	if err != nil {
		return agent.SystemMetrics{}, err
	}
	diskUsed, diskTotal, err := readDisk()
	if err != nil {
		return agent.SystemMetrics{}, err
	}
	rx, tx, err := readNetwork()
	if err != nil {
		return agent.SystemMetrics{}, err
	}
	loadData, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return agent.SystemMetrics{}, fmt.Errorf("读取系统负载失败: %w", err)
	}
	load, err := strconv.ParseFloat(strings.Fields(string(loadData))[0], 64)
	if err != nil {
		return agent.SystemMetrics{}, fmt.Errorf("解析系统负载失败: %w", err)
	}
	totalDelta := afterTotal - beforeTotal
	idleDelta := afterIdle - beforeIdle
	if totalDelta == 0 || idleDelta > totalDelta {
		return agent.SystemMetrics{}, errors.New("CPU 采样结果无效")
	}
	return agent.SystemMetrics{
		CPUPercent: 100 * float64(totalDelta-idleDelta) / float64(totalDelta), MemoryUsed: memoryUsed, MemoryTotal: memoryTotal,
		DiskUsed: diskUsed, DiskTotal: diskTotal, NetworkRX: rx, NetworkTX: tx, LoadOneMinute: load,
	}, nil
}

func readCPU() (uint64, uint64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, fmt.Errorf("读取 CPU 状态失败: %w", err)
	}
	fields := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, errors.New("CPU 状态格式无效")
	}
	var values []uint64
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, errors.New("CPU 状态数值无效")
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return total, idle, nil
}

func readMemory() (uint64, uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("读取内存状态失败: %w", err)
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			values[strings.TrimSuffix(fields[0], ":")] = value * 1024
		}
	}
	total, available := values["MemTotal"], values["MemAvailable"]
	if total == 0 || available > total {
		return 0, 0, errors.New("内存状态格式无效")
	}
	return total - available, total, nil
}

func readDisk() (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0, 0, fmt.Errorf("读取磁盘状态失败: %w", err)
	}
	total := stat.Blocks * uint64(stat.Bsize)
	available := stat.Bavail * uint64(stat.Bsize)
	return total - available, total, nil
}

func readNetwork() (uint64, uint64, error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, fmt.Errorf("读取网络状态失败: %w", err)
	}
	var rx, tx uint64
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		received, errRX := strconv.ParseUint(fields[0], 10, 64)
		transmitted, errTX := strconv.ParseUint(fields[8], 10, 64)
		if errRX != nil || errTX != nil {
			return 0, 0, errors.New("网络状态数值无效")
		}
		rx += received
		tx += transmitted
	}
	return rx, tx, nil
}
