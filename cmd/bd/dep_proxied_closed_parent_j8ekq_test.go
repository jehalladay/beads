//go:build cgo

package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedDepAddClosedParentGuard is the beads-j8ekq teeth: the direct
// dep-add path (dep.go, beads-eth8) refuses attaching an OPEN child to an
// already-CLOSED auto-closing parent via a parent-child edge — it would
// silently recreate the closed-parent-with-open-child inconsistency the
// close-guard family (2hkd/b0tw/eth8) forbids. The proxied handler
// (dep_proxied_server.go) mirrored the other three direct guards (isChildOf
// deadlock, IsValid/IsWellKnown, relates-to) but DROPPED this one, so a
// hub-connected crew's `bd dep add <open-child> <closed-parent> --type
// parent-child` landed the forbidden edge silently. This is the add-side
// sibling of beads-6fns (proxied reopen dropped the same guard).
//
// Mutation: delete the closed-parent guard block from addDepProxiedOne →
// the refuse cases below go from rc!=0 to rc=0 (RED, edge silently added).
func TestProxiedDepAddClosedParentGuard(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// (1) closed EPIC parent + open child: the eth8 case the direct path already
	//     refused, now enforced on the proxied path too. Overridable with --force.
	t.Run("open_child_to_closed_epic_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "jek1")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic", "-t", "epic")
		bdProxiedClose(t, bd, p.dir, epic.ID) // childless epic closes clean
		child := bdProxiedCreate(t, bd, p.dir, "Open child") // open
		out := bdProxiedDepFail(t, bd, p.dir, "add", child.ID, epic.ID, "--type", "parent-child")
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent dep-add guard, got:\n%s", out)
		}
		// The edge must not have landed.
		db := openProxiedDB(t, p)
		if readIsBlocked(t, db, child.ID) {
			t.Errorf("child should not be blocked — the parent-child edge must not have been added")
		}
		if got := readStatus(t, db, child.ID); got != types.StatusOpen {
			t.Errorf("child must stay open, got %q", got)
		}
	})

	t.Run("open_child_to_closed_epic_force_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "jek2")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic force", "-t", "epic")
		bdProxiedClose(t, bd, p.dir, epic.ID)
		child := bdProxiedCreate(t, bd, p.dir, "Open child force")
		// --force must override the guard and land the edge.
		bdProxiedDep(t, bd, p.dir, "add", child.ID, epic.ID, "--type", "parent-child", "--force")
	})

	// (2) MOLECULE parent (aw9x8 widening axis): a closed molecule root must be
	//     guarded on the proxied add path too, not just epics.
	t.Run("open_child_to_closed_molecule_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "jek3")
		mol := bdProxiedCreate(t, bd, p.dir, "Closed molecule", "-t", "molecule")
		bdProxiedClose(t, bd, p.dir, mol.ID) // childless molecule closes clean
		child := bdProxiedCreate(t, bd, p.dir, "Open mol child")
		out := bdProxiedDepFail(t, bd, p.dir, "add", child.ID, mol.ID, "--type", "parent-child")
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent dep-add guard for molecule parent, got:\n%s", out)
		}
	})

	// (3) OPEN epic parent (regression control): attaching an open child to an
	//     OPEN parent must still succeed — the guard fires only on a CLOSED
	//     parent. The fix must not block legitimate parent-child adds.
	t.Run("open_child_to_open_epic_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "jek4")
		epic := bdProxiedCreate(t, bd, p.dir, "Open epic", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Child under open epic")
		// bdProxiedDep fatals on non-zero exit, so a clean return proves the
		// guard did NOT fire on an OPEN parent. Confirm the edge landed via the
		// parent's dependents list (established pattern for parent-child adds).
		bdProxiedDep(t, bd, p.dir, "add", child.ID, epic.ID, "--type", "parent-child")
		out := bdProxiedDep(t, bd, p.dir, "list", epic.ID, "--direction", "up")
		if !strings.Contains(out, child.ID) {
			t.Errorf("child under an OPEN epic parent should appear as a dependent (edge added), got:\n%s", out)
		}
	})

	// (4) CLOSED child to closed parent (precision control): the guard only
	//     refuses an OPEN child. A CLOSED child attached to a closed parent does
	//     NOT leave an open child, so it must succeed (matches direct path's
	//     child.Status != StatusClosed condition).
	t.Run("closed_child_to_closed_epic_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "jek5")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic ok", "-t", "epic")
		bdProxiedClose(t, bd, p.dir, epic.ID)
		child := bdProxiedCreate(t, bd, p.dir, "Closed child")
		bdProxiedClose(t, bd, p.dir, child.ID)
		// closed child → closed parent: no open-child inconsistency, allowed.
		bdProxiedDep(t, bd, p.dir, "add", child.ID, epic.ID, "--type", "parent-child")
	})
}
