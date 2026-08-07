//go:build linux

package setuphelper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/wangshangbin/homestack/internal/maintenance"
)

func RunMaintenance(ctx context.Context, socketPath string, allowedUID uint32) error {
	if os.Geteuid() != 0 {
		return errors.New("维护 Helper 必须由 root 启动")
	}
	if socketPath == "" || allowedUID == 0 {
		return errors.New("维护 Helper 必须明确配置 Unix Socket 和非 root 调用 UID")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("监听维护 Helper Socket 失败: %w", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if err := os.Chown(socketPath, int(allowedUID), -1); err != nil {
		return err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return err
	}
	manager := NewMaintenanceManager()
	go func() { <-ctx.Done(); _ = listener.Close() }()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handleMaintenanceConnection(connection, allowedUID, manager)
	}
}

func handleMaintenanceConnection(connection net.Conn, allowedUID uint32, manager *MaintenanceManager) {
	defer connection.Close()
	response := maintenance.Response{}
	if err := authorize(connection, allowedUID); err != nil {
		response.Error = err.Error()
		_ = json.NewEncoder(connection).Encode(response)
		return
	}
	_ = connection.SetDeadline(time.Now().Add(time.Minute))
	var request maintenance.Request
	decoder := json.NewDecoder(io.LimitReader(connection, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.Error = "解析维护 Helper 请求失败: " + err.Error()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		switch request.Operation {
		case "configuration":
			config, err := manager.Configuration(ctx)
			response.Config = &config
			if err != nil {
				response.Error = err.Error()
				response.Config = nil
			}
		case "status":
			var err error
			response.Status, err = manager.Status(ctx)
			if err != nil {
				response.Error = err.Error()
			}
		case "reconfigure":
			if request.Config == nil {
				response.Error = "域名迁移缺少配置"
			} else {
				var err error
				response.Status, err = manager.Reconfigure(ctx, *request.Config)
				if err != nil {
					response.Error = err.Error()
				}
			}
		default:
			response.Error = "维护 Helper 操作不在白名单中"
		}
	}
	_ = json.NewEncoder(connection).Encode(response)
}
