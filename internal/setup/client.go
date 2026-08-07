package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

type Helper interface {
	Status(context.Context) (Status, error)
	Prepare(context.Context, Configuration) (Status, error)
	Finalize(context.Context) (Status, error)
}

type ConfigHelper interface {
	Configuration(context.Context) (PublicConfiguration, error)
	Reconfigure(context.Context, Configuration) (Status, error)
}

type SocketClient struct {
	Path string
}

func (c SocketClient) Status(ctx context.Context) (Status, error) {
	return c.call(ctx, HelperRequest{Operation: "status"})
}

func (c SocketClient) Prepare(ctx context.Context, config Configuration) (Status, error) {
	return c.call(ctx, HelperRequest{Operation: "prepare", Config: &config})
}

func (c SocketClient) Finalize(ctx context.Context) (Status, error) {
	return c.call(ctx, HelperRequest{Operation: "finalize"})
}

func (c SocketClient) Configuration(ctx context.Context) (PublicConfiguration, error) {
	status, err := c.call(ctx, HelperRequest{Operation: "configuration"})
	if err != nil {
		return PublicConfiguration{}, err
	}
	if status.Config == nil {
		return PublicConfiguration{}, errors.New("Config Helper 未返回 Control 配置")
	}
	return *status.Config, nil
}

func (c SocketClient) Reconfigure(ctx context.Context, config Configuration) (Status, error) {
	return c.call(ctx, HelperRequest{Operation: "reconfigure", Config: &config})
}

func (c SocketClient) call(ctx context.Context, request HelperRequest) (Status, error) {
	path := c.Path
	if path == "" {
		path = DefaultSocketPath
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return Status{}, fmt.Errorf("连接 Config Helper 失败: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Status{}, fmt.Errorf("发送 Config Helper 请求失败: %w", err)
	}
	var response HelperResponse
	decoder := json.NewDecoder(connection)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Status{}, fmt.Errorf("解析 Config Helper 响应失败: %w", err)
	}
	if response.Error != "" {
		return response.Status, errors.New(response.Error)
	}
	return response.Status, nil
}
