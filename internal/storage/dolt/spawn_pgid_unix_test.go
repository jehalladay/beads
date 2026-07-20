//go:build integration && !windows

package dolt

import (
	"os/exec"
	"syscall"
)

// setSpawnPgid puts a directly-spawned `dolt sql-server` into its own process
// group so the whole group can be signalled at cleanup. Without it, killing
// only cmd.Process leaves any descendant dolt processes orphaned when a test
// binary is externally SIGKILLed (go test -timeout) — the 55-reaped orphan
// class on the shared /fsx node (beads-yzrp / hq-sl5wbc leg B). Reference:
// internal/doltserver/socket_integration_test.go:61.
func setSpawnPgid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killSpawnGroup signals the process GROUP of a server started with
// setSpawnPgid (negative PID targets the group), reaping descendants that a
// bare Process.Kill would orphan. Falls back to killing the single process if
// the group signal fails (e.g. the child already exited). Safe on the shared
// node: it targets only this test's own spawned group, never prod/peer servers.
func killSpawnGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// The child is its own group leader (pgid == pid) because of Setpgid.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}
