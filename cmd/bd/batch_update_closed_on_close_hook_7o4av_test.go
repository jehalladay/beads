//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// beads-7o4av (CLOSE-PARITY matrix, batch-update close leg — sibling of vn7dl).
//
// A batch `update <id> status=closed` leg reaches the SAME terminal closed
// state as `bd close` and the single `bd update --status closed` path
// (beads-vn7dl), both of which fire the on_close hook. But the batch update
// leg dispatches through tx.UpdateIssue (runBatchOp "update" case) →
// hookTrackingTransaction.UpdateIssue, which records ONLY EventUpdate — so
// on_close automation silently did not run for a close-via-batch-update in
// embedded mode. (The batch "close" op is unaffected: it goes through
// tx.CloseIssue, which the tracking tx records as EventClose.)
//
// Driven END-TO-END through the ACTUAL `bd batch` subprocess — a tx-helper
// would false-green by skipping the post-commit hook plumbing entirely (the
// batch-parity family lesson). MUTATION-VERIFIED: remove the getHookRunner().
// RunSync(EventClose, after) loop added to batch.go and
// batch_update_status_closed_fires_on_close goes RED.
func TestBatchUpdateStatusClosedFiresOnCloseHook_7o4av(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// installHooks writes on_close + on_update marker scripts into the workspace
	// hooks dir; returns the two marker paths.
	installHooks := func(t *testing.T, beadsDir string) (onClose, onUpdate string) {
		t.Helper()
		hooksDir := filepath.Join(beadsDir, "hooks")
		if err := os.MkdirAll(hooksDir, 0755); err != nil {
			t.Fatalf("mkdir hooks: %v", err)
		}
		onClose = filepath.Join(beadsDir, "on_close_marker.txt")
		onUpdate = filepath.Join(beadsDir, "on_update_marker.txt")
		writeHook := func(name, marker string) {
			script := "#!/bin/sh\necho fired >> " + marker + "\n"
			if err := os.WriteFile(filepath.Join(hooksDir, name), []byte(script), 0755); err != nil {
				t.Fatalf("write %s hook: %v", name, err)
			}
		}
		writeHook("on_close", onClose)
		writeHook("on_update", onUpdate)
		return onClose, onUpdate
	}

	fired := func(marker string) bool {
		b, err := os.ReadFile(marker)
		return err == nil && len(strings.TrimSpace(string(b))) > 0
	}

	runBatch := func(t *testing.T, dir, script string) {
		t.Helper()
		c := exec.Command(bd, "batch")
		c.Dir = dir
		c.Env = bdEnv(dir)
		c.Stdin = strings.NewReader(script)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("bd batch failed: %v\n%s", err, out)
		}
	}

	// CONTROL: a batch `close` op fires on_close (authoritative behavior the
	// batch update leg must mirror; confirms the harness wires hooks).
	t.Run("batch_close_op_fires_on_close_control", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "bc")
		onClose, _ := installHooks(t, beadsDir)
		iss := bdCreate(t, bd, dir, "batch-close control", "--type", "task")

		runBatch(t, dir, "close "+iss.ID+" done\n")
		if !fired(onClose) {
			t.Errorf("batch `close` op did not fire on_close (control) — harness broken")
		}
	})

	// FIX: a batch `update <id> status=closed` on an open issue fires on_close
	// (beads-7o4av). RED before the fix: only on_update fired.
	t.Run("batch_update_status_closed_fires_on_close", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "bu")
		onClose, onUpdate := installHooks(t, beadsDir)
		iss := bdCreate(t, bd, dir, "batch-close-via-update", "--type", "task")

		runBatch(t, dir, "update "+iss.ID+" status=closed\n")
		if !fired(onClose) {
			t.Errorf("REGRESSION (beads-7o4av): batch `update <id> status=closed` on an open issue did NOT fire the on_close hook (fires it via `bd close`, batch `close`, and the single `bd update --status closed` — vn7dl) — on_close automation silently skipped in embedded mode")
		}
		if !fired(onUpdate) {
			t.Errorf("batch `update <id> status=closed` should still fire on_update (unchanged decorator behavior)")
		}
	})

	// NEGATIVE: re-closing an already-closed issue via batch update is a no-op
	// transition and must NOT fire on_close (guards the transition condition,
	// not blanket firing — parity with the single path's already-closed guard).
	t.Run("batch_update_already_closed_does_not_fire_on_close", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "bn")
		iss := bdCreate(t, bd, dir, "batch already-closed", "--type", "task")

		// Close first (before installing hooks so the marker starts clean).
		runBatch(t, dir, "close "+iss.ID+" first\n")

		onClose, _ := installHooks(t, beadsDir)
		// A second update status=closed on the already-closed issue: no
		// open→closed transition, so on_close must NOT fire.
		runBatch(t, dir, "update "+iss.ID+" status=closed\n")
		if fired(onClose) {
			t.Errorf("batch `update status=closed` on an ALREADY-closed issue should NOT fire on_close (no real transition), but the marker fired")
		}
	})
}
