//go:build integration && windows

package dolt

import "os/exec"

// setSpawnPgid is a no-op on Windows (no process groups via Setpgid).
func setSpawnPgid(cmd *exec.Cmd) {}

// killSpawnGroup falls back to a single-process kill on Windows.
func killSpawnGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
