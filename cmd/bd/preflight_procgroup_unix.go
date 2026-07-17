//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so the whole tree
// (go test spawns compile + test-binary children) can be signalled together.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to the child's entire process group. The
// negative PID targets the group leader and all its descendants, so no
// grandchild is left orphaned on timeout. Falls back to killing just the
// process if the group PID can't be resolved.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}
