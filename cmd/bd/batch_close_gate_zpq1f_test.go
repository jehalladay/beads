//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-zpq1f (batch-parity family, SWEEP-201): the single-close path
// (close.go) applies checkGateSatisfaction before closing — a machine-checkable
// gate (timer / gh:run / gh:pr) whose condition is UNMET is refused unless
// --force. The batch-close preflight (guardBatchCloses, batch.go) applied the
// epic-open-children + blocked-by-open guards but was MISSING the
// gate-satisfaction check, so `bd batch` silently closed an unexpired gate that
// `bd close` rejects — prematurely unblocking the gated work.
//
// This is the batch sibling of the beads-zgku/beads-1d08 close-time integrity
// guards. Uses a timer gate (no fake gh needed): an unexpired timer gate is the
// simplest machine-checkable-but-unresolved case.
//
// End-to-end through the ACTUAL `bd batch` subprocess (NOT a tx-helper, which
// would false-green by skipping the CLI-layer preflight entirely — see the
// batch-parity family lessons). MUTATION-VERIFIED: removing the
// checkGateSatisfaction call in guardBatchCloses lets the batch close the
// unexpired gate (rc=0, gate CLOSED).
func TestEmbeddedBatchCloseGateSatisfaction_zpq1f(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// helper: create an unexpired timer gate blocking a fresh target, return the
	// gate id.
	makeUnexpiredGate := func(t *testing.T, dir string) string {
		t.Helper()
		target := bdCreate(t, bd, dir, "Gate target for zpq1f", "--type", "task")
		mkGate := exec.Command(bd, "gate", "create", "--json", "--type", "timer", "--blocks", target.ID, "--timeout", "24h")
		mkGate.Dir = dir
		mkGate.Env = bdEnv(dir)
		out, err := mkGate.Output()
		if err != nil {
			t.Fatalf("gate create failed: %v\n%s", err, out)
		}
		gateID := parseIssueJSON(t, out).ID
		if gateID == "" {
			t.Fatalf("could not resolve gate id from: %s", out)
		}
		return gateID
	}

	// gateClosed reports whether the gate issue currently shows status closed.
	gateClosed := func(t *testing.T, dir, gateID string) bool {
		t.Helper()
		show := exec.Command(bd, "show", gateID, "--json")
		show.Dir = dir
		show.Env = bdEnv(dir)
		out, err := show.Output()
		if err != nil {
			t.Fatalf("bd show %s failed: %v\n%s", gateID, err, out)
		}
		return strings.Contains(string(out), `"status": "closed"`) ||
			strings.Contains(string(out), `"status":"closed"`)
	}

	// CONTROL: single `bd close <unexpired timer gate>` is refused and the gate
	// stays open. Establishes the authoritative behavior batch must mirror.
	t.Run("single_close_refuses_unexpired_gate", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "zc")
		gateID := makeUnexpiredGate(t, dir)

		closeCmd := exec.Command(bd, "close", gateID)
		closeCmd.Dir = dir
		closeCmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, closeCmd)
		combined := stdout.String() + stderr.String()
		if err == nil {
			t.Fatalf("expected single close to FAIL for unexpired gate, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "gate condition not satisfied") {
			t.Errorf("expected 'gate condition not satisfied' from single close, got:\n%s", combined)
		}
		if gateClosed(t, dir, gateID) {
			t.Errorf("single close of an unexpired gate should leave it OPEN, but it is closed")
		}
	})

	// FIX: `bd batch` close of the same unexpired gate must ALSO be refused
	// (atomic all-or-nothing → non-zero rc, gate stays open).
	t.Run("batch_close_refuses_unexpired_gate", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "zb")
		gateID := makeUnexpiredGate(t, dir)

		batchCmd := exec.Command(bd, "batch")
		batchCmd.Dir = dir
		batchCmd.Env = bdEnv(dir)
		batchCmd.Stdin = strings.NewReader("close " + gateID + " test\n")
		stdout, stderr, err := runCommandBuffers(t, batchCmd)
		combined := stdout.String() + stderr.String()
		if err == nil {
			t.Fatalf("expected batch close to FAIL for unexpired gate, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "gate condition not satisfied") {
			t.Errorf("expected 'gate condition not satisfied' from batch close, got:\n%s", combined)
		}
		if gateClosed(t, dir, gateID) {
			t.Errorf("batch close of an unexpired gate should leave it OPEN (zpq1f), but it is closed")
		}
	})

	// --force override: batch --force skips the gate guard (parity with
	// `bd close --force`), so the gate closes.
	t.Run("batch_force_overrides_gate", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "zf")
		gateID := makeUnexpiredGate(t, dir)

		batchCmd := exec.Command(bd, "batch", "--force")
		batchCmd.Dir = dir
		batchCmd.Env = bdEnv(dir)
		batchCmd.Stdin = strings.NewReader("close " + gateID + " forced\n")
		stdout, stderr, err := runCommandBuffers(t, batchCmd)
		combined := stdout.String() + stderr.String()
		if err != nil {
			t.Fatalf("expected batch --force to CLOSE the gate, got error: %v\n%s", err, combined)
		}
		if !gateClosed(t, dir, gateID) {
			t.Errorf("batch --force should close the unexpired gate, but it is still open:\n%s", combined)
		}
	})
}
