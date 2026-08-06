//go:build windows

package ccconnect

import "os"

func signalProcess(process *os.Process) error {
	return process.Kill()
}
