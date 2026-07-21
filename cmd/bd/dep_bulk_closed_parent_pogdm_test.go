//go:build cgo

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedBulkDepAddClosedParentGuard is the beads-pogdm teeth: the single
// `bd dep add <child> <parent> --type parent-child` enforces the eth8
// closed-parent guard (dep.go:450), but the BULK `bd dep add --file <edges>`
// path (addBulkDependencies → validateBulkDepEdges) dropped it — it ran the
// tx-core cycle check but called tx.AddDependencyWithOptions with NO
// closed-parent guard, so the exact edge the single path refuses landed
// silently via --file. Same bulk-path-drops-cmd-layer-guard class as
// beads-zpq1f (batch close skips gate-satisfaction) and beads-c2pr1 (batch
// skips audit-file).
//
// Fix wires the guard into validateBulkDepEdges (read-only pre-pass, batched
// error, --force override) using the shared isAutoClosingParentType (epic OR
// molecule OR ephemeral, beads-aw9x8). Mutation: pass force=true unconditionally
// into validateBulkDepEdges (or drop the guard block) → the refuse cases below
// go from rc!=0 to rc=0 (RED, edge silently lands).
func TestEmbeddedBulkDepAddClosedParentGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bdp")

	edgeLine := func(from, to string) string {
		return fmt.Sprintf(`{"from":%q,"to":%q,"type":"parent-child"}`+"\n", from, to)
	}

	// (1) BULK add of an open child to a CLOSED EPIC parent must be refused
	//     (the eth8 case the single path already refused, now on --file too).
	t.Run("bulk_open_child_to_closed_epic_refuses", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "closed epic", "--type", "epic")
		bdClose(t, bd, dir, epic.ID) // childless → clean close
		child := bdCreate(t, bd, dir, "open child", "--type", "task")
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		out := bdDepAddFail(t, bd, dir, "--file", file)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on bulk dep add, got:\n%s", out)
		}
		// The whole batch aborts with no partial write — edge must not exist.
		for _, d := range showDeps(t, bd, dir, child.ID) {
			if d.ID == epic.ID {
				t.Errorf("parent-child edge to closed epic must not have been added: %+v", d)
			}
		}
	})

	// (2) MOLECULE parent (aw9x8 widening axis) — the bulk guard must fire for a
	//     closed molecule root too, not just epics.
	t.Run("bulk_open_child_to_closed_molecule_refuses", func(t *testing.T) {
		mol := bdCreate(t, bd, dir, "closed molecule", "--type", "molecule")
		bdClose(t, bd, dir, mol.ID)
		child := bdCreate(t, bd, dir, "open mol child", "--type", "task")
		file := writeBulkFile(t, edgeLine(child.ID, mol.ID))
		out := bdDepAddFail(t, bd, dir, "--file", file)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on bulk dep add for molecule parent, got:\n%s", out)
		}
	})

	// (3) --force overrides the bulk guard and lands the edge.
	t.Run("bulk_open_child_to_closed_epic_force_succeeds", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "closed epic force", "--type", "epic")
		bdClose(t, bd, dir, epic.ID)
		child := bdCreate(t, bd, dir, "open child force", "--type", "task")
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		bdDepAdd(t, bd, dir, "--file", file, "--force")
		found := false
		for _, d := range showDeps(t, bd, dir, child.ID) {
			if d.ID == epic.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("--force should land the parent-child edge to the closed epic")
		}
	})

	// (4) OPEN epic parent (regression control): a bulk edge to an OPEN parent
	//     must still succeed — the guard fires only on a CLOSED parent.
	t.Run("bulk_open_child_to_open_epic_succeeds", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "open epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "child under open epic", "--type", "task")
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		bdDepAdd(t, bd, dir, "--file", file)
		found := false
		for _, d := range showDeps(t, bd, dir, child.ID) {
			if d.ID == epic.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("bulk edge under an OPEN epic parent should land")
		}
	})

	// (5) CLOSED child to closed parent (precision control): a closed child does
	//     not leave an open child, so it must succeed (matches the single path's
	//     child.Status != StatusClosed condition).
	t.Run("bulk_closed_child_to_closed_epic_succeeds", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "closed epic ok", "--type", "epic")
		bdClose(t, bd, dir, epic.ID)
		child := bdCreate(t, bd, dir, "closed child", "--type", "task")
		bdClose(t, bd, dir, child.ID)
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		bdDepAdd(t, bd, dir, "--file", file)
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusClosed {
			t.Errorf("closed child must stay closed, got %s", got.Status)
		}
	})
}
