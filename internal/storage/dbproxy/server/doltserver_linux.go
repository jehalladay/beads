//go:build linux

package server

import (
	"os/exec"
	"syscall"
)

// applyKillOnParentDeath sets the child's parent-death signal to SIGKILL so the
// Linux kernel reaps the spawned dolt sql-server the moment THIS process dies,
// even by SIGKILL / panic / `go test -timeout` — none of which run deferred
// Stop() or cancel the managing context. This is the orphan-proof backstop for
// beads-sl5wbc; it is only applied when a caller opts in via
// SetKillOnParentDeath (test harnesses), never for the production detached
// proxied-server child.
func applyKillOnParentDeath(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
