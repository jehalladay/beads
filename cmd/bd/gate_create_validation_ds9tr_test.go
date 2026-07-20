//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestGateCreateRejectsUnresolvableGates is the teeth for beads-ds9tr: bd gate
// create must reject gate-type invariants that gate check can never resolve —
// otherwise the blocked issue is stranded out of bd ready forever. Previously
// create validated only the timeout FORMAT:
//   - type=timer with no --timeout: accepted (rc=0), then every gate check emits
//     "no timeout set" forever.
//   - arbitrary --type (e.g. banana): accepted (rc=0), gate check silently skips
//     it (default: continue) — no error, no resolution, issue blocked forever.
//
// A malformed --timeout IS rejected at create, so the type/timer gaps are an
// asymmetry, not by-design.
func TestGateCreateRejectsUnresolvableGates(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "gv")

	store := openStore(t, beadsDir, "gv")
	if err := store.SetConfig(t.Context(), "types.custom", `["gate"]`); err != nil {
		t.Fatalf("SetConfig types.custom: %v", err)
	}
	store.Close()

	t.Run("timer_without_timeout_rejected", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task timer-no-timeout", "--type", "task")
		out := bdGateFail(t, bd, dir, "create", "--type", "timer", "--blocks", task.ID)
		if !strings.Contains(out, "requires --timeout") {
			t.Errorf("expected timer-requires-timeout rejection, got: %s", out)
		}
	})

	t.Run("invalid_type_rejected", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task bad-type", "--type", "task")
		out := bdGateFail(t, bd, dir, "create", "--type", "banana", "--blocks", task.ID)
		if !strings.Contains(out, "invalid gate type") {
			t.Errorf("expected invalid-gate-type rejection, got: %s", out)
		}
	})

	t.Run("default_human_still_accepted", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task human", "--type", "task")
		out := bdGate(t, bd, dir, "create", "--blocks", task.ID)
		if !strings.Contains(out, "Created gate") {
			t.Errorf("default human gate must still be accepted: %s", out)
		}
	})

	t.Run("timer_with_timeout_accepted", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task timer-ok", "--type", "task")
		out := bdGate(t, bd, dir, "create", "--type", "timer", "--timeout", "2h", "--blocks", task.ID)
		if !strings.Contains(out, "Created gate") {
			t.Errorf("timer+timeout must be accepted: %s", out)
		}
	})

	t.Run("ghpr_accepted", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task ghpr", "--type", "task")
		out := bdGate(t, bd, dir, "create", "--type", "gh:pr", "--await-id", "42", "--blocks", task.ID)
		if !strings.Contains(out, "Created gate") {
			t.Errorf("gh:pr must be accepted: %s", out)
		}
	})

	// beads-9jtzh: a gh:pr gate with no --await-id can never resolve (checkGHPR
	// returns "no PR number specified" forever; no discover path fills it, unlike
	// gh:run) → the blocked issue is stranded out of bd ready. Reject at create,
	// mirroring the timer-requires-timeout guard.
	t.Run("ghpr_without_await_id_rejected", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task ghpr-no-await", "--type", "task")
		out := bdGateFail(t, bd, dir, "create", "--type", "gh:pr", "--blocks", task.ID)
		if !strings.Contains(out, "requires --await-id") {
			t.Errorf("expected gh:pr-requires-await-id rejection, got: %s", out)
		}
	})

	// The gh:run sibling must stay accepted with no --await-id — discover fills
	// await_id post-create, so this is by-design, not a strand (guards the fix
	// against over-broadening the requirement to gh:run).
	t.Run("ghrun_without_await_id_still_accepted", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task ghrun-no-await", "--type", "task")
		out := bdGate(t, bd, dir, "create", "--type", "gh:run", "--blocks", task.ID)
		if !strings.Contains(out, "Created gate") {
			t.Errorf("gh:run without --await-id must still be accepted (discover fills it): %s", out)
		}
	})
}
