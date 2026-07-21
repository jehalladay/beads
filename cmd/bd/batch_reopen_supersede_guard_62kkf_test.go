//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-62kkf (batch-parity family; the batch twin of beads-50dto): the single
// reopen path (reopen.go:146-167) and `bd update --status open` (beads-50dto)
// both refuse reopening a closed issue that is SUPERSEDED (beads-8sjb3) or a
// DUPLICATE (beads-8nugc) — supersedes/duplicates are NON-blocking edges, so
// reopening leaves a contradictory "open but superseded/duplicate" issue that
// REAPPEARS in `bd ready` as actionable work. The batch preflight
// (guardBatchReopens, batch.go) is the third leg of that surface. Before this
// fix it wired ONLY the closed-epic-parent check (beads-nf1k1), so `bd batch
// update <id> status=open` wrote straight to tx.UpdateIssue below the CLI guard
// layer and silently landed the inconsistency (rc=0, source OPEN + edge intact).
//
// End-to-end through the ACTUAL `bd batch` subprocess (NOT a tx-helper, which
// would false-green by skipping the CLI-layer preflight entirely — the
// batch-parity family lesson). MUTATION-VERIFIED: removing the
// supersededByTargets/duplicatesTargets checks from guardBatchReopens lets the
// batch reopen the source (rc=0, source OPEN).
func TestEmbeddedBatchReopenSupersedeDuplicateGuard_62kkf(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	runBatch := func(t *testing.T, dir, stdin string, extraArgs ...string) (combined string, err error) {
		t.Helper()
		args := append([]string{"batch"}, extraArgs...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader(stdin)
		stdout, stderr, e := runCommandBuffers(t, cmd)
		return stdout.String() + stderr.String(), e
	}

	// makeSuperseded builds the 8sjb3 scenario: `bd supersede old --with new`
	// adds a supersedes edge (old->new) and closes old. Returns oldID.
	makeSuperseded := func(t *testing.T, dir string) string {
		t.Helper()
		old := bdCreate(t, bd, dir, "62kkf superseded", "--type", "task")
		newer := bdCreate(t, bd, dir, "62kkf replacement", "--type", "task")
		bdSupersede(t, bd, dir, old.ID, "--with", newer.ID)
		if got := bdShow(t, bd, dir, old.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: superseded %s should be closed, got %s", old.ID, got.Status)
		}
		return old.ID
	}

	// makeDuplicate builds the 8nugc scenario: `bd duplicate old --of canonical`
	// adds a duplicates edge (old->canonical) and closes old. Returns oldID.
	makeDuplicate := func(t *testing.T, dir string) string {
		t.Helper()
		old := bdCreate(t, bd, dir, "62kkf duplicate", "--type", "task")
		canonical := bdCreate(t, bd, dir, "62kkf canonical", "--type", "task")
		bdDuplicate(t, bd, dir, old.ID, "--of", canonical.ID)
		if got := bdShow(t, bd, dir, old.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: duplicate %s should be closed, got %s", old.ID, got.Status)
		}
		return old.ID
	}

	// CONTROL: single `bd update <old> --status open` is refused for a superseded
	// issue (the authoritative behavior batch must mirror, from beads-50dto).
	t.Run("single_update_reopen_refuses_superseded", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cs")
		oldID := makeSuperseded(t, dir)

		out := bdUpdateFail(t, bd, dir, oldID, "--status", "open")
		if !strings.Contains(out, "superseded") {
			t.Errorf("expected a 'superseded' guard error from single update, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, oldID); got.Status != types.StatusClosed {
			t.Errorf("single reopen of a superseded issue should leave it CLOSED, got %s", got.Status)
		}
	})

	// FIX: `bd batch update <old> status=open` of a superseded issue must ALSO be
	// refused (non-zero rc, source stays closed).
	t.Run("batch_update_reopen_refuses_superseded", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "bs")
		oldID := makeSuperseded(t, dir)

		combined, err := runBatch(t, dir, "update "+oldID+" status=open\n")
		if err == nil {
			t.Fatalf("expected batch reopen of a superseded issue to FAIL, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "superseded") {
			t.Errorf("expected a 'superseded' guard error from batch reopen, got:\n%s", combined)
		}
		if got := bdShow(t, bd, dir, oldID); got.Status != types.StatusClosed {
			t.Errorf("batch reopen of a superseded issue should leave it CLOSED (62kkf), got %s", got.Status)
		}
	})

	// FIX: `bd batch update <old> status=open` of a duplicate must ALSO be
	// refused (non-zero rc, source stays closed).
	t.Run("batch_update_reopen_refuses_duplicate", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "bd")
		oldID := makeDuplicate(t, dir)

		combined, err := runBatch(t, dir, "update "+oldID+" status=open\n")
		if err == nil {
			t.Fatalf("expected batch reopen of a duplicate to FAIL, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "duplicate") {
			t.Errorf("expected a 'duplicate' guard error from batch reopen, got:\n%s", combined)
		}
		if got := bdShow(t, bd, dir, oldID); got.Status != types.StatusClosed {
			t.Errorf("batch reopen of a duplicate should leave it CLOSED (62kkf), got %s", got.Status)
		}
	})

	// --force override: batch --force skips the guard (parity with
	// `bd update --status open --force`), so the superseded issue reopens.
	t.Run("batch_force_overrides_supersede_guard", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "bf")
		oldID := makeSuperseded(t, dir)

		combined, err := runBatch(t, dir, "update "+oldID+" status=open\n", "--force")
		if err != nil {
			t.Fatalf("expected batch --force to reopen the superseded issue, got error: %v\n%s", err, combined)
		}
		if got := bdShow(t, bd, dir, oldID); got.Status != types.StatusOpen {
			t.Errorf("batch --force should reopen a superseded issue, got %s\n%s", got.Status, combined)
		}
	})

	// Negative (no false positive): reopening a plainly-closed issue with NO
	// supersedes/duplicates edge is unaffected.
	t.Run("batch_reopen_plain_closed_still_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "bp")
		iss := bdCreate(t, bd, dir, "62kkf plain closed", "--type", "task")
		bdClose(t, bd, dir, iss.ID)
		if got := bdShow(t, bd, dir, iss.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: issue should be closed, got %s", got.Status)
		}

		combined, err := runBatch(t, dir, "update "+iss.ID+" status=open\n")
		if err != nil {
			t.Fatalf("reopen of a plain-closed issue (no supersede/dup edge) must be allowed, got error: %v\n%s", err, combined)
		}
		if got := bdShow(t, bd, dir, iss.ID); got.Status != types.StatusOpen {
			t.Errorf("plain-closed issue should reopen, got %s", got.Status)
		}
	})
}
