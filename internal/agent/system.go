package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const DefaultHelperSocket = "/run/homestack/helper.sock"

type SystemMetrics struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryUsed    uint64  `json:"memory_used"`
	MemoryTotal   uint64  `json:"memory_total"`
	DiskUsed      uint64  `json:"disk_used"`
	DiskTotal     uint64  `json:"disk_total"`
	NetworkRX     uint64  `json:"network_rx"`
	NetworkTX     uint64  `json:"network_tx"`
	LoadOneMinute float64 `json:"load_one_minute"`
}

type ServiceStatus struct {
	ID      string   `json:"id"`
	State   string   `json:"state"`
	Detail  string   `json:"detail,omitempty"`
	Actions []string `json:"actions"`
}

type LogEntry struct {
	Cursor    string    `json:"cursor"`
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
	Message   string    `json:"message"`
}

type LogPage struct {
	Entries    []LogEntry `json:"entries"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type SystemManager interface {
	Metrics(context.Context) (SystemMetrics, error)
	Services(context.Context) ([]ServiceStatus, error)
	Action(context.Context, string, string) error
	Logs(context.Context, string, int, string) (LogPage, error)
}

type HelperRequest struct {
	Operation string `json:"operation"`
	Service   string `json:"service,omitempty"`
	Action    string `json:"action,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

type HelperResponse struct {
	Metrics  *SystemMetrics  `json:"metrics,omitempty"`
	Services []ServiceStatus `json:"services,omitempty"`
	Logs     *LogPage        `json:"logs,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type SystemClient struct {
	SocketPath string
}

func NewSystemClient(socketPath string) (*SystemClient, error) {
	if socketPath == "" {
		return nil, errors.New("helper Unix Socket 路径不能为空")
	}
	return &SystemClient{SocketPath: socketPath}, nil
}

func (c *SystemClient) Metrics(ctx context.Context) (SystemMetrics, error) {
	response, err := c.call(ctx, HelperRequest{Operation: "metrics"})
	if err != nil {
		return SystemMetrics{}, err
	}
	if response.Metrics == nil {
		return SystemMetrics{}, errors.New("helper 未返回资源监控结果")
	}
	return *response.Metrics, nil
}

func (c *SystemClient) Services(ctx context.Context) ([]ServiceStatus, error) {
	response, err := c.call(ctx, HelperRequest{Operation: "services"})
	if err != nil {
		return nil, err
	}
	return response.Services, nil
}

func (c *SystemClient) Action(ctx context.Context, service, action string) error {
	_, err := c.call(ctx, HelperRequest{Operation: "action", Service: service, Action: action})
	return err
}

func (c *SystemClient) Logs(ctx context.Context, service string, limit int, cursor string) (LogPage, error) {
	response, err := c.call(ctx, HelperRequest{Operation: "logs", Service: service, Limit: limit, Cursor: cursor})
	if err != nil {
		return LogPage{}, err
	}
	if response.Logs == nil {
		return LogPage{}, errors.New("helper 未返回日志结果")
	}
	return *response.Logs, nil
}

func (c *SystemClient) call(ctx context.Context, request HelperRequest) (HelperResponse, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return HelperResponse{}, fmt.Errorf("连接 HomeStack helper 失败: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return HelperResponse{}, fmt.Errorf("发送 helper 请求失败: %w", err)
	}
	var response HelperResponse
	if err := json.NewDecoder(io.LimitReader(connection, 4<<20)).Decode(&response); err != nil {
		return HelperResponse{}, fmt.Errorf("读取 helper 响应失败: %w", err)
	}
	if response.Error != "" {
		return HelperResponse{}, errors.New(response.Error)
	}
	return response, nil
}
