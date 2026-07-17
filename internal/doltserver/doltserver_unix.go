//go:build !windows

package doltserver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// portConflictHint is the platform-specific command to diagnose port conflicts.
// Used in error messages when a port is busy but the occupying process can't be identified.
const portConflictHint = "lsof -i :%d"

// processListHint is the platform-specific command to list dolt processes.
// Used in error messages when too many dolt servers are running.
const processListHint = "pgrep -la 'dolt sql-server'"

// procAttrDetached returns SysProcAttr to detach a child process from the parent
// process group so it survives parent exit.
func procAttrDetached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// findPIDOnPort returns the PID of the process listening on a TCP port.
// Uses lsof to look up the listener. Returns 0 if no process found or on error.
func findPIDOnPort(port int) int {
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port), "-sTCP:LISTEN").Output() //nolint:gosec // G702: port is internal int, not user input
	if err != nil {
		return 0
	}
	// lsof may return multiple PIDs; take the first one
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
			return pid
		}
	}
	return 0
}

// listDoltProcessPIDs returns PIDs of all running dolt sql-server processes.
// Excludes zombies and defunct processes. Callers derive count (len) and
// membership (linear scan) from the returned slice.
//
// The pgrep pattern uses `dolt.*sql-server` rather than the literal
// `dolt sql-server` so it still matches when top-level dolt flags appear
// between the binary name and the subcommand — e.g. debug mode launches
// the server as `dolt --prof cpu --prof-path /…/dolt-pprof sql-server …`,
// in which `dolt sql-server` no longer appears as a contiguous substring.
// Without this, IsRunning's isDoltProcess check would reject the PID as
// "not a dolt process" and wipe the PID/port files of a healthy server,
// breaking `bd dolt status` and auto-start reattachment. The per-PID
// substring check below still requires both "dolt" and "sql-server" in
// the cmdline, so this only widens the first-stage filter; it does not
// loosen identity validation.
func listDoltProcessPIDs() []int {
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
		// Exclude zombies: ps -o state= returns Z for zombie, X for dead
		stateOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "state=").Output()
		if err != nil {
			continue
		}
		state := strings.TrimSpace(string(stateOut))
		if len(state) > 0 && (state[0] == 'Z' || state[0] == 'X') {
			continue
		}
		// Verify command line contains both "dolt" and "sql-server"
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

// isProcessInDir checks if a process's working directory matches the given path.
// Uses lsof to look up the CWD, which is more reliable than checking command-line
// args since dolt sql-server is started with cmd.Dir (not a --data-dir flag).
func isProcessInDir(pid int, dir string) bool {
	// On macOS, lsof requires -a to AND selectors together; without it,
	// "-p <pid>" and "-d cwd" can yield cwd entries from unrelated processes.
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return false
	}
	// Resolve symlinks on the target: lsof reports the kernel-RESOLVED cwd
	// (symlink-free), so a dir that is/contains a symlink (e.g. /fsx .dolt-data
	// symlinks, macOS /tmp->/private/tmp) would otherwise never match and the
	// process would be misjudged as NOT in dir (beads-g4f0).
	resolvedDir := resolvePathForCompare(dir)
	// lsof -Fn output format: "p<pid>\nfcwd\nn<path>"
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			cwd := strings.TrimSpace(line[1:])
			if resolvePathForCompare(cwd) == resolvedDir {
				return true
			}
		}
	}
	return false
}

// resolvePathForCompare returns the fully symlink-resolved absolute path for
// comparing a process cwd (which lsof reports symlink-resolved) against a
// configured directory (which may contain symlink components). Falls back to
// filepath.Abs when EvalSymlinks fails (e.g. the path no longer exists), so a
// vanished dir still compares by its absolute form rather than silently
// mismatching.
func resolvePathForCompare(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	abs, _ := filepath.Abs(p)
	return abs
}

// isProcessAlive checks if a process with the given PID is running.
// Uses signal 0 which doesn't send a signal but checks process existence.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// gracefulStop sends SIGTERM, waits for the process to exit, then SIGKILL if needed.
// Used by reclaimPort and StopWithForce where data has already been flushed.
func gracefulStop(pid int, timeout time.Duration) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to PID %d: %w", pid, err)
	}

	// Poll for exit
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if process.Signal(syscall.Signal(0)) != nil {
			return nil // exited
		}
	}

	// Still running — force kill, then VERIFY death before reporting success.
	// SIGKILL cannot be caught but is not instantaneous: a process in an
	// uninterruptible sleep (D state — e.g. a dolt sql-server mid-fsync on a
	// slow/contended /fsx mount) does not die until its syscall returns, which
	// can exceed a fixed sleep. Returning nil unconditionally lets callers
	// (StopWithForce/IsRunning) wipe the PID/port tracking of a process that is
	// still alive and still holding its port. Mirror the SIGTERM poll above and
	// only return success once the process is actually gone (bd-jvki).
	_ = process.Signal(syscall.SIGKILL)
	killDeadline := time.Now().Add(timeout)
	for time.Now().Before(killDeadline) {
		time.Sleep(100 * time.Millisecond)
		if !isProcessAlive(pid) {
			return nil // confirmed dead
		}
	}
	return fmt.Errorf("process %d still alive %s after SIGKILL (likely uninterruptible I/O); not confirming death", pid, timeout)
}
