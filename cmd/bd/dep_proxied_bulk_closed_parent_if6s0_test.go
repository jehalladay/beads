//go:build cgo

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedBulkDepAddClosedParentGuard is the beads-if6s0 teeth: the 4th and
// last leg of the eth8 closed-parent guard. The guard ("a closed auto-closing
// parent must not gain an open child") is enforced on:
//   - direct single: dep.go (eth8 + aw9x8 widening)
//   - proxied single: dep_proxied_server.go addDepProxiedOne (beads-j8ekq)
//   - direct bulk:    validateBulkDepEdges (beads-pogdm)
//   - proxied bulk:   runDepAddBulkProxied — THIS was uncovered: it only called
//     AddDependencies with no closed-parent check, so a hub-connected crew's
//     `bd dep add --file <edges>` with a parent-child edge onto a closed
//     epic/molecule/wisp root landed the forbidden edge silently.
//
// Fix mirrors the single-path guard into runDepAddBulkProxied using the shared
// proxiedResolveDepEndpoint + isAutoClosingParentType, refusing the whole batch
// (no partial write) unless --force. Mutation: remove/disable the guard loop →
// the refuse cases below go from rc!=0 to rc=0 (RED, edge silently lands).
func TestProxiedBulkDepAddClosedParentGuard(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	edgeLine := func(from, to string) string {
		return fmt.Sprintf(`{"from":%q,"to":%q,"type":"parent-child"}`+"\n", from, to)
	}

	// (1) BULK add of an open child to a CLOSED EPIC parent must be refused over
	//     the proxied path (the eth8 case, now on the proxied --file leg too).
	t.Run("bulk_open_child_to_closed_epic_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "if1")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic", "-t", "epic")
		bdProxiedClose(t, bd, p.dir, epic.ID) // childless epic closes clean
		child := bdProxiedCreate(t, bd, p.dir, "Open child")
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		out := bdProxiedDepFail(t, bd, p.dir, "add", "--file", file)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on proxied bulk dep add, got:\n%s", out)
		}
		// Whole batch aborts with no partial write — edge must not exist.
		db := openProxiedDB(t, p)
		if readIsBlocked(t, db, child.ID) {
			t.Errorf("child should not be blocked — the parent-child edge must not have been added")
		}
		if got := readStatus(t, db, child.ID); got != types.StatusOpen {
			t.Errorf("child must stay open, got %q", got)
		}
	})

	// (2) MOLECULE parent (aw9x8 widening axis): the proxied bulk guard must fire
	//     for a closed molecule root too, not just epics.
	t.Run("bulk_open_child_to_closed_molecule_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "if2")
		mol := bdProxiedCreate(t, bd, p.dir, "Closed molecule", "-t", "molecule")
		bdProxiedClose(t, bd, p.dir, mol.ID)
		child := bdProxiedCreate(t, bd, p.dir, "Open mol child")
		file := writeBulkFile(t, edgeLine(child.ID, mol.ID))
		out := bdProxiedDepFail(t, bd, p.dir, "add", "--file", file)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on proxied bulk dep add for molecule parent, got:\n%s", out)
		}
	})

	// (3) --force overrides the proxied bulk guard and lands the edge.
	t.Run("bulk_open_child_to_closed_epic_force_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "if3")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic force", "-t", "epic")
		bdProxiedClose(t, bd, p.dir, epic.ID)
		child := bdProxiedCreate(t, bd, p.dir, "Open child force")
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		bdProxiedDep(t, bd, p.dir, "add", "--file", file, "--force")
		out := bdProxiedDep(t, bd, p.dir, "list", epic.ID, "--direction", "up")
		if !strings.Contains(out, child.ID) {
			t.Errorf("--force should land the parent-child edge to the closed epic, got:\n%s", out)
		}
	})

	// (4) OPEN epic parent (regression control): a bulk edge to an OPEN parent
	//     must still succeed — the guard fires only on a CLOSED parent.
	t.Run("bulk_open_child_to_open_epic_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "if4")
		epic := bdProxiedCreate(t, bd, p.dir, "Open epic", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Child under open epic")
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		bdProxiedDep(t, bd, p.dir, "add", "--file", file)
		out := bdProxiedDep(t, bd, p.dir, "list", epic.ID, "--direction", "up")
		if !strings.Contains(out, child.ID) {
			t.Errorf("bulk edge under an OPEN epic parent should land, got:\n%s", out)
		}
	})

	// (5) CLOSED child to closed parent (precision control): a closed child does
	//     not leave an open child, so it must succeed (matches the single path's
	//     child.Status != StatusClosed condition).
	t.Run("bulk_closed_child_to_closed_epic_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "if5")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic ok", "-t", "epic")
		bdProxiedClose(t, bd, p.dir, epic.ID)
		child := bdProxiedCreate(t, bd, p.dir, "Closed child")
		bdProxiedClose(t, bd, p.dir, child.ID)
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		bdProxiedDep(t, bd, p.dir, "add", "--file", file)
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, child.ID); got != types.StatusClosed {
			t.Errorf("closed child must stay closed, got %q", got)
		}
	})
}
