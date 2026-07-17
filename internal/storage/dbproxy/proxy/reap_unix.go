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
	// Resolve symlinks on the target: lsof reports the kernel-RESOLVED cwd
	// (symlink-free), so a rootDir that is/contains a symlink (e.g. the /fsx
	// .dolt-data symlink) would otherwise never match and the orphaned dolt
	// would be skipped — recurring the pu8c port-wedge (beads-g4f0).
	resolvedDir := resolvePathForCompare(dir)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			if resolvePathForCompare(strings.TrimSpace(line[1:])) == resolvedDir {
				return true
			}
		}
	}
	return false
}

// resolvePathForCompare returns the fully symlink-resolved absolute path for
// comparing a process cwd (lsof reports it symlink-resolved) against a
// configured directory (which may contain symlink components). Falls back to
// filepath.Abs when EvalSymlinks fails (e.g. the path no longer exists).
func resolvePathForCompare(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	abs, _ := filepath.Abs(p)
	return abs
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
