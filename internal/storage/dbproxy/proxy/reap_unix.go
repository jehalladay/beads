//go:build !windows

package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// defaultListDoltServerPIDs returns the PIDs of running dolt sql-server
// processes, excluding zombies/dead. Mirrors internal/doltserver's
// listDoltProcessPIDs (see reap.go consolidation note): the pgrep pattern
// `dolt.*sql-server` tolerates top-level dolt flags between the binary and the
// subcommand (e.g. debug mode `dolt --prof cpu ... sql-server`), and the
// per-PID cmdline check still requires both "dolt" and "sql-server".
func defaultListDoltServerPIDs() []int {
	out, err := exec.Command("pgrep", "-f", "dolt.*sql-server").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 {
			continue
		}
		stateOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "state=").Output()
		if err != nil {
			continue
		}
		if state := strings.TrimSpace(string(stateOut)); len(state) > 0 && (state[0] == 'Z' || state[0] == 'X') {
			continue
		}
		cmdOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if err != nil {
			continue
		}
		cmdline := strings.TrimSpace(string(cmdOut))
		if strings.Contains(cmdline, "dolt") && strings.Contains(cmdline, "sql-server") {
			pids = append(pids, pid)
		}
	}
	return pids
}

// defaultDoltProcessInDir reports whether pid's working directory is dir.
// Mirrors internal/doltserver's isProcessInDir: lsof -a ANDs the -p/-d
// selectors (required on macOS), and the CWD is compared as an absolute path.
func defaultDoltProcessInDir(pid int, dir string) bool {
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return false
	}
	absDir, _ := filepath.Abs(dir)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			absCwd, _ := filepath.Abs(strings.TrimSpace(line[1:]))
			if absCwd == absDir {
				return true
			}
		}
	}
	return false
}

// defaultStopProcess sends SIGTERM, polls for exit up to timeout, then SIGKILL.
// Mirrors internal/doltserver's gracefulStop.
func defaultStopProcess(pid int, timeout time.Duration) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if process.Signal(syscall.Signal(0)) != nil {
			return nil // exited
		}
	}
	_ = process.Signal(syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	return nil
}
