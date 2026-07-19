//go:build linux

package server

import (
	"os/exec"
	"syscall"
	"testing"
)

// TestApplyKillOnParentDeath_SetsPdeathsig is the teeth for beads-sl5wbc: on
// Linux the test-harness opt-in must set the child's parent-death signal to
// SIGKILL so a spawned dolt sql-server is reaped even when the parent dies by
// SIGKILL / panic / `go test -timeout` (none of which run deferred Stop).
func TestApplyKillOnParentDeath_SetsPdeathsig(t *testing.T) {
	cmd := exec.Command("true")
	applyKillOnParentDeath(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("applyKillOnParentDeath left SysProcAttr nil — child would not be reaped on parent death")
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Pdeathsig = %v, want SIGKILL", cmd.SysProcAttr.Pdeathsig)
	}
}

// TestApplyKillOnParentDeath_PreservesExistingSysProcAttr ensures the helper
// augments rather than clobbers a caller-provided SysProcAttr.
func TestApplyKillOnParentDeath_PreservesExistingSysProcAttr(t *testing.T) {
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	applyKillOnParentDeath(cmd)
	if !cmd.SysProcAttr.Setpgid {
		t.Error("applyKillOnParentDeath clobbered an existing SysProcAttr field (Setpgid)")
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Pdeathsig = %v, want SIGKILL", cmd.SysProcAttr.Pdeathsig)
	}
}

// TestKillOnParentDeath_DefaultsOff guards the production invariant: the
// proxied-server child (cmd/bd/db_proxy_child.go) is deliberately detached and
// must SURVIVE its transient parent, so a freshly constructed DoltServer must
// NOT opt into kill-on-parent-death. Only test harnesses flip it on.
func TestKillOnParentDeath_DefaultsOff(t *testing.T) {
	s := &DoltServer{}
	if s.killOnParentDeath {
		t.Fatal("killOnParentDeath defaulted ON — would kill the production detached proxied-server child on parent exit")
	}
	s.SetKillOnParentDeath(true)
	if !s.killOnParentDeath {
		t.Fatal("SetKillOnParentDeath(true) did not enable the flag")
	}
}
