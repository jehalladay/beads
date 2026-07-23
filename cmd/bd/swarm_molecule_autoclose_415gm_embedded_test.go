//go:build cgo

package main

import (
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedSwarmMoleculeAutoCloses is the beads-415gm regression tooth.
//
// `bd swarm create` mints a swarm coordinator molecule (IssueType="molecule",
// MolType=MolTypeSwarm) linked to its epic via DepRelatesTo (swarm.go). The
// swarm work-items are parent-child children of the EPIC, not the swarm
// molecule — so when every work-item closes (100% progress) the completion
// signal walks the parent-child chain up to the epic (a non-auto-closing root)
// and never reaches the swarm molecule, which stayed OPEN forever, diverging
// from `bd mol pour` molecules that auto-close at completion.
//
// The fix (autoCloseCompletedSwarmMolecule in close.go, mirrored in the proxied
// twin) resolves the relates-to-linked swarm molecule when the last work-item
// closes and closes it at 100%. This tooth builds a real swarm, closes all
// work-items, and asserts the swarm molecule reaches "closed". Mutation-verify:
// with the fix reverted the swarm molecule stays "open" and this fails RED.
func TestEmbeddedSwarmMoleculeAutoCloses(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sam")

	// Epic with 3 work-items (c3 depends on c1, a serial edge).
	epicID, childIDs := createSwarmableEpic(t, bd, dir, "AutoClose")

	// Mint the swarm coordinator molecule for this epic.
	created := bdSwarmJSON(t, bd, dir, "create", epicID)
	swarmID, _ := created["swarm_id"].(string)
	if swarmID == "" {
		t.Fatalf("expected a swarm_id from bd swarm create, got %v", created)
	}

	// Baseline: the swarm molecule is a molecule-typed swarm and starts open.
	sw := bdShow(t, bd, dir, swarmID)
	if sw.IssueType != types.TypeMolecule || sw.MolType != types.MolTypeSwarm {
		t.Fatalf("expected a swarm molecule (type=molecule, moltype=swarm), got type=%s moltype=%s", sw.IssueType, sw.MolType)
	}
	if sw.Status == types.StatusClosed {
		t.Fatalf("swarm molecule %s should start open, got %s", swarmID, sw.Status)
	}

	// Close every work-item. c3 is blocked by c1, so close c1 first; the last
	// close drives the epic to 100% completion and should trip the swarm
	// molecule auto-close.
	c1, c2, c3 := childIDs[0], childIDs[1], childIDs[2]
	bdClose(t, bd, dir, c1)
	bdClose(t, bd, dir, c2)
	bdClose(t, bd, dir, c3)

	// The teeth: the completed swarm molecule must now be closed, matching the
	// pour-molecule auto-close behavior. Pre-fix, findParentMolecule only walks
	// parent-child (→ epic, which stays open) so the swarm molecule stayed open.
	got := bdShow(t, bd, dir, swarmID)
	if got.Status != types.StatusClosed {
		t.Fatalf("beads-415gm: swarm molecule %s should auto-close once all work-items complete, got status=%s (epic=%s children=%v)",
			swarmID, got.Status, epicID, childIDs)
	}

	// The epic itself is NOT a molecule and must remain open (ordinary epics
	// become explicitly close-eligible, they are not closed as a side effect) —
	// guards against the fix over-reaching and closing the epic.
	gotEpic := bdShow(t, bd, dir, epicID)
	if gotEpic.Status == types.StatusClosed {
		t.Fatalf("beads-415gm: epic %s must stay open (only the swarm molecule auto-closes), got %s", epicID, gotEpic.Status)
	}
}
