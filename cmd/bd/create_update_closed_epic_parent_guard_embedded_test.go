//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestClosedEpicParentAssignmentGuard is the teeth for beads-a8a1b: the
// forbidden "closed epic with an OPEN child" invariant was guarded on the
// status-transition axes (close epic / reopen child — zgku/b0tw) but WIDE OPEN
// on the PARENT-ASSIGNMENT axis. Two paths reached it:
//
//	(1) create <child> --parent <closed-epic>  (create.go only checked EXISTS)
//	(2) update <open-child> --parent <closed-epic> (reparent had no status check)
//
// Both must now refuse (rc!=0) unless --force, mirroring `bd close --force`.
func TestClosedEpicParentAssignmentGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ce")

	// Helper: make a closed parent of the given type (create, close — no open
	// children yet so the close is allowed).
	makeClosedParent := func(t *testing.T, title, typ string) string {
		t.Helper()
		p := bdCreate(t, bd, dir, title, "--type", typ)
		bdClose(t, bd, dir, p.ID)
		got := bdShow(t, bd, dir, p.ID)
		if got.Status != types.StatusClosed {
			t.Fatalf("setup: %s %s should be closed, got %s", typ, p.ID, got.Status)
		}
		return p.ID
	}
	makeClosedEpic := func(t *testing.T, title string) string {
		t.Helper()
		return makeClosedParent(t, title, "epic")
	}

	// (1) create --parent <closed epic> is refused. beads-czu1s generalized the
	//     error text "closed epic" -> "closed parent".
	t.Run("create_child_under_closed_epic_refused", func(t *testing.T) {
		epicID := makeClosedEpic(t, "a8a1b closed epic create")
		out := bdCreateFail(t, bd, dir, "orphan child", "--type", "task", "--parent", epicID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected a 'closed parent' guard error, got: %s", out)
		}
	})

	// (1-force) --force overrides the create guard.
	t.Run("create_child_under_closed_epic_force_overrides", func(t *testing.T) {
		epicID := makeClosedEpic(t, "a8a1b closed epic create force")
		child := bdCreate(t, bd, dir, "forced child", "--type", "task", "--parent", epicID, "--force")
		if child.ID == "" {
			t.Errorf("--force should allow creating a child under a closed epic")
		}
	})

	// (1-czu1s) create --parent <closed MOLECULE> is refused — the aw9x8
	//     widening axis the create guard had missed (was bare TypeEpic). A closed
	//     molecule/wisp root is an auto-closing parent, so an open child under it
	//     is the same forbidden inconsistency. Mutation: reverting create.go's
	//     predicate to bare TypeEpic turns this RED (child created, rc=0).
	t.Run("create_child_under_closed_molecule_refused", func(t *testing.T) {
		molID := makeClosedParent(t, "czu1s closed molecule create", "molecule")
		out := bdCreateFail(t, bd, dir, "mol orphan child", "--type", "task", "--parent", molID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected a 'closed parent' guard error under a closed molecule, got: %s", out)
		}
	})

	// (1-czu1s-force) --force overrides the molecule create guard too.
	t.Run("create_child_under_closed_molecule_force_overrides", func(t *testing.T) {
		molID := makeClosedParent(t, "czu1s closed molecule create force", "molecule")
		child := bdCreate(t, bd, dir, "forced mol child", "--type", "task", "--parent", molID, "--force")
		if child.ID == "" {
			t.Errorf("--force should allow creating a child under a closed molecule")
		}
	})

	// (1-czu1s-neg) create --parent <OPEN molecule> still succeeds — the guard
	//     fires only on a CLOSED parent, so the widening must not over-fire on an
	//     open molecule root.
	t.Run("create_child_under_open_molecule_still_allowed", func(t *testing.T) {
		openMol := bdCreate(t, bd, dir, "czu1s open molecule", "--type", "molecule")
		child := bdCreate(t, bd, dir, "child under open molecule", "--type", "task", "--parent", openMol.ID)
		if child.ID == "" {
			t.Errorf("creating a child under an OPEN molecule should succeed")
		}
	})

	// (2) update <open child> --parent <closed epic> is refused.
	t.Run("reparent_open_child_under_closed_epic_refused", func(t *testing.T) {
		epicID := makeClosedEpic(t, "a8a1b closed epic reparent")
		child := bdCreate(t, bd, dir, "loose task", "--type", "task")
		out := bdUpdateFail(t, bd, dir, child.ID, "--parent", epicID)
		if !strings.Contains(out, "closed epic") {
			t.Errorf("expected a 'closed epic' guard error on reparent, got: %s", out)
		}
	})

	// (2-force) --force overrides the reparent guard.
	t.Run("reparent_open_child_under_closed_epic_force_overrides", func(t *testing.T) {
		epicID := makeClosedEpic(t, "a8a1b closed epic reparent force")
		child := bdCreate(t, bd, dir, "loose task force", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--parent", epicID, "--force")
	})

	// Negative: reparenting under an OPEN epic is unaffected (no false-positive).
	t.Run("reparent_under_open_epic_still_allowed", func(t *testing.T) {
		openEpic := bdCreate(t, bd, dir, "a8a1b open epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "task to open epic", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--parent", openEpic.ID)
	})
}
