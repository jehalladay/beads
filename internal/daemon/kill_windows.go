//go:build windows

package daemon

import (
	"fmt"
	"os/exec"
	"strconv"
)

func killProcess(pid int) error {
	// Use taskkill on Windows
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to kill PID %d: %w", pid, err)
	}
	return nil
}
