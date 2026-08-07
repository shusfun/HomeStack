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

func (c SocketClient) call(ctx context.Context, request HelperRequest) (Status, error) {
	path := c.Path
	if path == "" {
		path = DefaultSocketPath
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return Status{}, fmt.Errorf("连接 Setup Helper 失败: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Status{}, fmt.Errorf("发送 Setup Helper 请求失败: %w", err)
	}
	var response HelperResponse
	decoder := json.NewDecoder(connection)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Status{}, fmt.Errorf("解析 Setup Helper 响应失败: %w", err)
	}
	if response.Error != "" {
		return response.Status, errors.New(response.Error)
	}
	return response.Status, nil
}
