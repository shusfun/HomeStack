package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type LocalSystemManager struct{}

func (m *LocalSystemManager) Metrics(ctx context.Context) (SystemMetrics, error) {
	return localMetrics(ctx)
}

func (m *LocalSystemManager) Services(ctx context.Context) ([]ServiceStatus, error) {
	result := make([]ServiceStatus, 0, 2)
	for _, service := range []struct {
		id      string
		address string
	}{{"filebrowser", "127.0.0.1:19445"}, {"jellyfin", "127.0.0.1:19446"}} {
		state, detail := "inactive", "回环端口未监听"
		dialer := net.Dialer{Timeout: 800 * time.Millisecond}
		connection, err := dialer.DialContext(ctx, "tcp", service.address)
		if err == nil {
			state, detail = "active", service.address
			_ = connection.Close()
		}
		result = append(result, ServiceStatus{ID: service.id, State: state, Detail: detail, Actions: []string{}})
	}
	return result, nil
}

func (m *LocalSystemManager) Action(context.Context, string, string) error {
	return errors.New("桌面托管组件由 HomeStack Node 自动管理，不接受网页生命周期操作")
}

func (m *LocalSystemManager) Logs(_ context.Context, service string, limit int, cursor string) (LogPage, error) {
	if limit < 1 || limit > 500 || cursor != "" {
		return LogPage{}, errors.New("桌面日志只支持读取最新 1 到 500 行")
	}
	stateDir := os.Getenv("HOMESTACK_AGENT_STATE_DIR")
	if !filepath.IsAbs(stateDir) {
		return LogPage{}, errors.New("Node 状态目录无效")
	}
	paths := map[string]string{
		"homestack-agent": filepath.Join(stateDir, "node.stderr.log"),
		"filebrowser":     filepath.Join(stateDir, "managed", "filebrowser", "filebrowser.log"),
		"jellyfin":        filepath.Join(stateDir, "managed", "jellyfin", "jellyfin.log"),
	}
	path, ok := paths[service]
	if !ok {
		return LogPage{}, errors.New("日志服务 ID 不在白名单中")
	}
	file, err := os.Open(path)
	if err != nil {
		return LogPage{}, fmt.Errorf("读取 %s 日志失败: %w", service, err)
	}
	defer file.Close()
	lines := make([]string, 0, limit)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		lines = append(lines, redactLog(scanner.Text()))
		if len(lines) > limit {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return LogPage{}, err
	}
	page := LogPage{Entries: make([]LogEntry, 0, len(lines))}
	for index, line := range lines {
		page.Entries = append(page.Entries, LogEntry{Cursor: strconv.Itoa(index), Timestamp: time.Time{}, Service: service, Message: line})
	}
	return page, nil
}

func redactLog(value string) string {
	for _, prefix := range []string{"token=", "api_key=", "password="} {
		lower := strings.ToLower(value)
		if index := strings.Index(lower, prefix); index >= 0 {
			end := strings.IndexAny(value[index+len(prefix):], " \t,;")
			if end < 0 {
				return value[:index+len(prefix)] + "[REDACTED]"
			}
			end += index + len(prefix)
			value = value[:index+len(prefix)] + "[REDACTED]" + value[end:]
		}
	}
	return value
}
