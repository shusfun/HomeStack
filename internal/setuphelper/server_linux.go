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
	"syscall"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

func Run(ctx context.Context, socketPath string, allowedUID uint32) error {
	if os.Geteuid() != 0 {
		return errors.New("homestack-config-helper 必须由 root 启动")
	}
	if socketPath == "" || allowedUID == 0 {
		return errors.New("Config Helper 必须明确配置 Unix Socket 和非 root 调用 UID")
	}
	if err := os.MkdirAll("/run/homestack-config", 0o755); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("监听 Config Helper Socket 失败: %w", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if err := os.Chown(socketPath, int(allowedUID), -1); err != nil {
		return err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return err
	}
	manager := NewManager()
	go func() { <-ctx.Done(); _ = listener.Close() }()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handleConnection(connection, allowedUID, manager)
	}
}

func handleConnection(connection net.Conn, allowedUID uint32, manager *Manager) {
	defer connection.Close()
	response := setupapi.HelperResponse{}
	if err := authorize(connection, allowedUID); err != nil {
		response.Error = err.Error()
		_ = json.NewEncoder(connection).Encode(response)
		return
	}
	_ = connection.SetDeadline(time.Now().Add(10 * time.Minute))
	var request setupapi.HelperRequest
	decoder := json.NewDecoder(io.LimitReader(connection, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.Error = "解析 Config Helper 请求失败: " + err.Error()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
		defer cancel()
		switch request.Operation {
		case "status":
			response.Status, response.Error = result(manager.Status())
		case "prepare":
			if request.Config == nil {
				response.Error = "Prepare 缺少配置"
			} else {
				response.Status, response.Error = result(manager.Prepare(ctx, *request.Config))
			}
		case "finalize":
			response.Status, response.Error = result(manager.Finalize(ctx))
		case "configuration":
			config, err := manager.Configuration()
			response.Status = setupapi.Status{Phase: setupapi.PhaseCompleted, Config: &config, UpdatedAt: time.Now().UTC()}
			if err != nil {
				response.Error = err.Error()
			}
		case "reconfigure":
			if request.Config == nil {
				response.Error = "Reconfigure 缺少配置"
			} else {
				response.Status, response.Error = result(manager.Reconfigure(ctx, *request.Config))
			}
		default:
			response.Error = "Config Helper 操作不在白名单中"
		}
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func authorize(connection net.Conn, allowedUID uint32) error {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return errors.New("Config Helper 只接受 Unix Socket")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return err
	}
	var credential *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	if credential == nil || credential.Uid != allowedUID {
		return errors.New("Config Helper 拒绝非 Control 用户连接")
	}
	return nil
}

func result(status setupapi.Status, err error) (setupapi.Status, string) {
	if err != nil {
		return status, err.Error()
	}
	return status, ""
}
