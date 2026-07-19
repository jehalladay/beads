//go:build !linux

package server

import "os/exec"

// applyKillOnParentDeath is a no-op on non-Linux platforms: Pdeathsig is
// Linux-specific. macOS/Windows test runs rely on the deferred Stop()/t.Cleanup
// teardown (and the CWD-based reaper) instead. Kept as a platform stub so the
// opt-in call site in Start() compiles everywhere (beads-sl5wbc).
func applyKillOnParentDeath(_ *exec.Cmd) {}
