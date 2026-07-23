//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedMoleculeAutoCloseDoneCategory is the behavioral teeth for
// beads-bpc9y: proxiedGetMoleculeProgress (close_proxied_server.go) counted a
// molecule step toward Completed ONLY on the literal types.StatusClosed, so a
// step moved to a custom DONE-CATEGORY status was silently NOT counted. The
// proxied autoclose gate (proxiedTryAutoCloseMolecule -> "if progress.Completed
// < progress.Total { return }") then never fired for a molecule whose remaining
// steps were all in a done-category status — diverging from the direct-path
// autoCloseCompletedMolecule (done-aware via beads-x463g), the storage-layer
// GetMoleculeProgressInTx (beads-bobpm), and bd ready/count/list.
//
// Scenario: template-labeled molecule root with two steps. Register a custom
// done-category status ("resolved:done"), move step1 into it, then close step2.
// After the fix step1 counts as Completed, so closing the final open step
// completes the molecule and the proxied cascade auto-closes the root.
//
// Before the fix: root stays open (step1 uncounted -> Completed(1) < Total(2)).
// MUTATION-VERIFY: drop the `|| doneStatusNames[string(issue.Status)]` leg in
// proxiedGetMoleculeProgress -> this test goes RED (root stays open).
//
// This runs the real proxied-server subprocess end-to-end (bd init
// --proxied-server -> config set status.custom -> update -> close), which is the
// only way to exercise the tx-scoped uw.ConfigUseCase().GetCustomStatuses read
// the fix relies on; a pure/marshal test would not cover the proxied UOW path.
func TestProxiedMoleculeAutoCloseDoneCategory(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	p := bdProxiedInit(t, bd, "pmdc")

	// Register a custom done-category status: "resolved" is terminal/done.
	bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")

	root := bdProxiedCreate(t, bd, p.dir, "Molecule root", "-t", "epic", "--labels", "template")
	s1 := bdProxiedCreate(t, bd, p.dir, "Step 1", "--parent", root.ID)
	s2 := bdProxiedCreate(t, bd, p.dir, "Step 2", "--parent", root.ID)

	// Move step1 into the custom done-category status (NOT literal 'closed').
	bdProxiedUpdateOne(t, bd, p.dir, s1.ID, "--status", "resolved")

	// Close the last literally-open step. With step1 correctly counted as
	// complete, the molecule is now fully complete -> the cascade auto-closes
	// the root.
	bdProxiedClose(t, bd, p.dir, s2.ID)

	db := openProxiedDB(t, p)
	if got := readStatus(t, db, s1.ID); got != types.Status("resolved") {
		t.Fatalf("step1 should be in the custom done status 'resolved', got %q", got)
	}
	if got := readStatus(t, db, root.ID); got != types.StatusClosed {
		t.Errorf("molecule root should auto-close once all steps are complete — a custom done-category step must count toward completion in proxied mode (beads-bpc9y); got %q", got)
	}
}
