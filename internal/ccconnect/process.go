package ccconnect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/wangshangbin/homestack/internal/components"
)

type ProcessManager struct {
	mu         sync.Mutex
	binary     string
	configPath string
	output     io.Writer
	command    *exec.Cmd
	lastError  error
}

func NewProcessManager(binary, configPath string, output io.Writer) *ProcessManager {
	return &ProcessManager{binary: binary, configPath: configPath, output: output}
}

func (m *ProcessManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.command != nil && m.command.Process != nil {
		return errors.New("cc-connect 已在运行")
	}
	spec, err := components.FindSpec("cc-connect")
	if err != nil {
		return err
	}
	spec.Binary = m.binary
	if err := components.RequireVersion(ctx, spec); err != nil {
		return err
	}
	command := exec.Command(m.binary, "--config", m.configPath)
	command.Stdout = m.output
	command.Stderr = m.output
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动 cc-connect 失败: %w", err)
	}
	m.command = command
	m.lastError = nil
	go m.wait(command)
	return nil
}

func (m *ProcessManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	command := m.command
	m.mu.Unlock()
	if command == nil || command.Process == nil {
		return nil
	}
	if err := signalProcess(command.Process); err != nil {
		return fmt.Errorf("停止 cc-connect 失败: %w", err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		running := m.command == command
		m.mu.Unlock()
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			if err := command.Process.Kill(); err != nil {
				return fmt.Errorf("强制停止 cc-connect 失败: %w", err)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *ProcessManager) Status() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.command != nil, m.lastError
}

func (m *ProcessManager) wait(command *exec.Cmd) {
	err := command.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.command == command {
		m.command = nil
		m.lastError = err
	}
}
