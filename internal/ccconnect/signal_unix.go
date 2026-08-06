//go:build !windows

package ccconnect

import (
	"os"
	"syscall"
)

func signalProcess(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}
