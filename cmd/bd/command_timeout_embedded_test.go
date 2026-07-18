//go:build cgo

package main

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// beads-cine: verify the subprocess hang-guard. commandBuffersTimeout parses
// BEADS_TEST_CMD_TIMEOUT and defaults to 90s; the watchdog in runCommandBuffers
// must kill a hung subprocess well before the 10m suite timeout so a single
// deadlocked proxied bd can't freeze the whole gate.
func TestCommandBuffersTimeoutEnvParsing(t *testing.T) {
	orig, had := os.LookupEnv("BEADS_TEST_CMD_TIMEOUT")
	defer func() {
		if had {
			_ = os.Setenv("BEADS_TEST_CMD_TIMEOUT", orig)
		} else {
			_ = os.Unsetenv("BEADS_TEST_CMD_TIMEOUT")
		}
	}()

	_ = os.Unsetenv("BEADS_TEST_CMD_TIMEOUT")
	if got := commandBuffersTimeout(); got != 90*time.Second {
		t.Errorf("default: got %s, want 90s", got)
	}

	_ = os.Setenv("BEADS_TEST_CMD_TIMEOUT", "3s")
	if got := commandBuffersTimeout(); got != 3*time.Second {
		t.Errorf("override 3s: got %s", got)
	}

	// Invalid / non-positive values fall back to the default.
	_ = os.Setenv("BEADS_TEST_CMD_TIMEOUT", "garbage")
	if got := commandBuffersTimeout(); got != 90*time.Second {
		t.Errorf("garbage should fall back to 90s, got %s", got)
	}
	_ = os.Setenv("BEADS_TEST_CMD_TIMEOUT", "0s")
	if got := commandBuffersTimeout(); got != 90*time.Second {
		t.Errorf("0s should fall back to 90s, got %s", got)
	}
}

// TestCommandBuffersHangGuardKillsFast proves the watchdog kills a hung
// subprocess quickly instead of waiting out the suite timeout. It reproduces
// the guard's select/kill logic against a real long-sleeping process (mirroring
// runCommandBuffers, which cannot be called directly here because it t.Fatalf's
// on timeout).
func TestCommandBuffersHangGuardKillsFast(t *testing.T) {
	// A subprocess that would "hang" far longer than our tiny bound.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep subprocess: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	start := time.Now()
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-done:
		t.Fatal("sleep 60 returned before the guard fired — unexpected")
	case <-timer.C:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done // reap
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("hang guard took %s to kill a hung subprocess; must be ~bound, not the full sleep", elapsed)
	}
}
