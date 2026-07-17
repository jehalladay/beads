//go:build !unix

package main

import "os/exec"

// setProcessGroup is a no-op on non-unix platforms (process-group semantics
// differ; Windows would need a Job object). The context timeout still kills
// the direct child via cmd.Cancel's default.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the direct child. Grandchildren are not group-reaped
// on non-unix platforms, matching the pre-existing behavior there.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
