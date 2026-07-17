//go:build windows

package proxy

import (
	"os"
	"time"
)

// Windows lacks pgrep/lsof; orphan-reap by CWD is not implemented here. The
// standalone internal/doltserver supervisor is the primary lifecycle owner on
// Windows, and dolt is launched in its own process group (see endpoint_windows.go).
func defaultListDoltServerPIDs() []int { return nil }

func defaultDoltProcessInDir(_ int, _ string) bool { return false }

func defaultStopProcess(pid int, _ time.Duration) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
