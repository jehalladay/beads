//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-50dto: `bd update --status open` reaches the SAME closed->open terminal
// state as `bd reopen` (beads-6qo8t) but wired ONLY the closed-epic-parent guard
// (b0tw), dropping the supersede (8sjb3) and duplicate (8nugc) reopen guards
// `bd reopen` enforces (reopen.go:146-167). Since supersedes/duplicates are
// NON-blocking edges, reopening a superseded/duplicate issue via update
// recreated the contradictory "open but superseded/duplicate" state and made the
// issue REAPPEAR in `bd ready`. These are the DIRECT-path teeth (proxied leg has
// its own *_proxied_* test). MUTATION-VERIFIED: removing either guard in
// update.go lets `bd update --status open` reopen the superseded/duplicate issue
// (rc=0, status open).
func TestEmbeddedUpdateOpenReopenGuards_50dto(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "uor")

	// CONTROL: `bd reopen` refuses a superseded issue (establishes the behavior
	// update must mirror).
	t.Run("control_reopen_superseded_refused", func(t *testing.T) {
		old := bdCreate(t, bd, dir, "sup old ctl", "--type", "task")
		nw := bdCreate(t, bd, dir, "sup new ctl", "--type", "task")
		bdSupersede(t, bd, dir, old.ID, "--with", nw.ID)
		out := bdReopenFail(t, bd, dir, old.ID)
		if !strings.Contains(out, "superseded by") {
			t.Errorf("control: expected reopen to refuse superseded issue, got: %s", out)
		}
	})

	// FIX: `bd update --status open` on a superseded issue must ALSO be refused.
	t.Run("update_open_superseded_refused", func(t *testing.T) {
		old := bdCreate(t, bd, dir, "sup old", "--type", "task")
		nw := bdCreate(t, bd, dir, "sup new", "--type", "task")
		bdSupersede(t, bd, dir, old.ID, "--with", nw.ID)

		out := bdUpdateFail(t, bd, dir, old.ID, "--status", "open")
		if !strings.Contains(out, "superseded by") {
			t.Errorf("expected a 'superseded by' guard error from update --status open, got: %s", out)
		}
		if got := bdShow(t, bd, dir, old.ID); got.Status != types.StatusClosed {
			t.Errorf("update --status open on a superseded issue must leave it CLOSED (50dto), got %s", got.Status)
		}
	})

	// FIX: `bd update --status open` on a duplicate issue must ALSO be refused.
	t.Run("update_open_duplicate_refused", func(t *testing.T) {
		dup := bdCreate(t, bd, dir, "dup old", "--type", "task")
		canonical := bdCreate(t, bd, dir, "dup canonical", "--type", "task")
		bdDuplicate(t, bd, dir, dup.ID, "--of", canonical.ID)

		out := bdUpdateFail(t, bd, dir, dup.ID, "--status", "open")
		if !strings.Contains(out, "duplicate of") {
			t.Errorf("expected a 'duplicate of' guard error from update --status open, got: %s", out)
		}
		if got := bdShow(t, bd, dir, dup.ID); got.Status != types.StatusClosed {
			t.Errorf("update --status open on a duplicate issue must leave it CLOSED (50dto), got %s", got.Status)
		}
	})

	// --force override parity with `bd reopen --force`: skips both guards.
	t.Run("update_open_force_overrides", func(t *testing.T) {
		old := bdCreate(t, bd, dir, "sup old force", "--type", "task")
		nw := bdCreate(t, bd, dir, "sup new force", "--type", "task")
		bdSupersede(t, bd, dir, old.ID, "--with", nw.ID)

		bdUpdate(t, bd, dir, old.ID, "--status", "open", "--force")
		if got := bdShow(t, bd, dir, old.ID); got.Status != types.StatusOpen {
			t.Errorf("update --status open --force should reopen the superseded issue, got %s", got.Status)
		}
	})

	// Regression (no false positive): a plain closed issue with no supersedes/
	// duplicates edge still reopens via update --status open.
	t.Run("update_open_plain_closed_unaffected", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "plain closed", "--type", "task")
		bdClose(t, bd, dir, iss.ID)
		bdUpdate(t, bd, dir, iss.ID, "--status", "open")
		if got := bdShow(t, bd, dir, iss.ID); got.Status != types.StatusOpen {
			t.Errorf("plain reopen via update (no supersedes/duplicates) should succeed, got %s", got.Status)
		}
	})
}
