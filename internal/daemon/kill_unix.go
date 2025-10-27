//go:build unix

package daemon

import (
	"fmt"
	"syscall"
)

func killProcess(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to PID %d: %w", pid, err)
	}
	return nil
}
