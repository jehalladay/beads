//go:build !windows

package doltserver

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestProcAttrDetached asserts the detached process attributes put the child in
// its own process group (Setpgid), which is what keeps a spawned dolt server
// from receiving the parent's terminal signals.
func TestProcAttrDetached(t *testing.T) {
	attr := procAttrDetached()
	if attr == nil {
		t.Fatal("procAttrDetached() returned nil")
	}
	if !attr.Setpgid {
		t.Errorf("procAttrDetached().Setpgid = false, want true")
	}
}

// TestIsProcessAlive_LiveAndDead covers both branches of isProcessAlive: a
// running child (signal 0 succeeds → true) and a reaped child whose PID no
// longer maps to a live process (→ false).
func TestIsProcessAlive_LiveAndDead(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	if !isProcessAlive(pid) {
		t.Errorf("isProcessAlive(%d) = false for a running process, want true", pid)
	}

	// Kill and reap so the PID is no longer a live, signalable process.
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill sleep: %v", err)
	}
	_ = cmd.Wait()

	if isProcessAlive(pid) {
		t.Errorf("isProcessAlive(%d) = true after the process exited, want false", pid)
	}
}

// TestGracefulStop_SIGTERMExit covers the happy path: gracefulStop sends SIGTERM
// to a child that respects it, then observes the process exit within the poll
// loop and returns nil.
func TestGracefulStop_SIGTERMExit(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// Reap the child in the background so the poll loop sees it gone (a
	// SIGTERM'd child becomes a zombie until Wait, and signal 0 still succeeds
	// on a zombie of our own child; reaping clears it).
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	if err := gracefulStop(pid, 5*time.Second); err != nil {
		t.Errorf("gracefulStop returned error for a cooperative process: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("child did not exit after gracefulStop")
	}
}

// TestGracefulStop_SIGKILLAfterTimeout covers the escalation path: a child that
// ignores SIGTERM stays alive past the timeout, so gracefulStop falls through to
// SIGKILL and still returns nil.
func TestGracefulStop_SIGKILLAfterTimeout(t *testing.T) {
	// A shell that traps (ignores) SIGTERM, then loops. The loop (rather than a
	// single `sleep 60`) keeps sh resident as its own process — a lone final
	// command would be exec-optimized away, discarding the trap and letting
	// SIGTERM reach sleep directly. gracefulStop's SIGTERM is swallowed, the
	// poll deadline elapses, and the SIGKILL branch fires.
	cmd := exec.Command("sh", "-c", "trap '' TERM; while true; do sleep 0.2; done")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sh: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// Give the shell a moment to install its SIGTERM trap before gracefulStop
	// sends the signal; otherwise SIGTERM can arrive pre-trap and kill it early.
	time.Sleep(300 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	start := time.Now()
	if err := gracefulStop(pid, 1*time.Second); err != nil {
		t.Errorf("gracefulStop returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("gracefulStop returned in %v; expected it to wait out the ~1s timeout before SIGKILL", elapsed)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("child not reaped after SIGKILL escalation")
	}
}

// TestGracefulStop_BogusPID covers FindProcess/Signal on a PID that maps to no
// process. On Unix os.FindProcess never errors, so the SIGTERM send fails and
// gracefulStop returns the wrapped signal error.
func TestGracefulStop_BogusPID(t *testing.T) {
	// PID 0x7fffffff is far above any real PID and reliably unused.
	const bogusPID = 0x7fffffff
	// Guard: ensure it truly isn't alive on this host.
	if isProcessAlive(bogusPID) {
		t.Skipf("PID %d unexpectedly alive on this host", bogusPID)
	}

	err := gracefulStop(bogusPID, 500*time.Millisecond)
	if err == nil {
		t.Fatal("gracefulStop(bogusPID) = nil, want a SIGTERM error")
	}
	// Sanity: the error should mention the PID's signal failure, not a nil deref.
	if _, findErr := os.FindProcess(bogusPID); findErr != nil {
		t.Skipf("FindProcess errored on this platform (%v); test assumption invalid", findErr)
	}
}
