package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

type SocketClient struct {
	Path string
}

func (c SocketClient) Configuration(ctx context.Context) (Configuration, error) {
	response, err := c.call(ctx, Request{Operation: "configuration"})
	if err != nil {
		return Configuration{}, err
	}
	if response.Config == nil {
		return Configuration{}, errors.New("维护 Helper 未返回当前配置")
	}
	return *response.Config, nil
}

func (c SocketClient) Status(ctx context.Context) (Status, error) {
	response, err := c.call(ctx, Request{Operation: "status"})
	return response.Status, err
}

func (c SocketClient) Reconfigure(ctx context.Context, config Configuration) (Status, error) {
	response, err := c.call(ctx, Request{Operation: "reconfigure", Config: &config})
	return response.Status, err
}

func (c SocketClient) call(ctx context.Context, request Request) (Response, error) {
	path := c.Path
	if path == "" {
		path = DefaultSocketPath
	}
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", path)
	if err != nil {
		return Response{}, fmt.Errorf("连接维护 Helper 失败: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Response{}, fmt.Errorf("发送维护 Helper 请求失败: %w", err)
	}
	var response Response
	decoder := json.NewDecoder(connection)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Response{}, fmt.Errorf("解析维护 Helper 响应失败: %w", err)
	}
	if response.Error != "" {
		return response, errors.New(response.Error)
	}
	return response, nil
}
