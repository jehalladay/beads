//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-4v7eb: `bd epic close-eligible` closes each eligible epic via
// renderEpicCloseEligible's closeFn (direct store.CloseIssue / proxied
// issueUC.CloseIssue), but that bare close DROPPED the
// autoCloseCompletedMolecule cascade that `bd close` runs (close.go:223). So
// when a close-eligible epic is itself the FINAL open step of an auto-closing
// molecule/wisp root, closing the epic left the parent root stuck OPEN
// (orphaned-completed-root) — the same molecule-autoclose-parity divergence
// batch (8cxe6) / update --status closed (zzp26) / supersede-dup (26gea) exist
// to close, on the epic-close-eligible leg. Distinct from iwzua (the
// audit-file-trail leg on the same chokepoint, already landed @9fcf65796).
//
// End-to-end through the real `bd` subprocess (cgo/embedded dolt) — the cascade
// lives only in the real cmd handler. Reuses seedMoleculeLastStepOpen's sibling
// helpers (bdCreate/bdDep/bdClose/bdShow/bdEpic) from the family tests.
//
// MUTATION-VERIFIED: removing the autoCloseCompletedMolecule hop added to
// renderEpicCloseEligible's direct closeFn → this test goes RED (the epic step
// closes but the molecule root stays OPEN).
func TestEpicCloseEligible_AutoClosesCompletedMolecule_4v7eb(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ecm")

	// Seed an auto-closing molecule root with two steps:
	//   step1 = a plain task, closed normally.
	//   step2 = an EPIC whose only child is closed → the epic is eligible for
	//           closure but stays open until `epic close-eligible` runs.
	// The epic is the molecule's LAST open step, so closing it via
	// close-eligible must cascade-close the molecule root.
	root := bdCreate(t, bd, dir, "molecule root", "--type", "molecule")
	step1 := bdCreate(t, bd, dir, "step 1 task", "--type", "task")
	epicStep := bdCreate(t, bd, dir, "step 2 epic", "--type", "epic")
	bdDep(t, bd, dir, "add", step1.ID, root.ID, "--type", "parent-child")
	bdDep(t, bd, dir, "add", epicStep.ID, root.ID, "--type", "parent-child")

	// The epic's own child, closed → makes the epic eligible for close-eligible.
	epicChild := bdCreate(t, bd, dir, "epic child", "--type", "task")
	bdDep(t, bd, dir, "add", epicChild.ID, epicStep.ID, "--type", "parent-child")
	bdClose(t, bd, dir, epicChild.ID)

	// Close the plain first step so only the epic step remains open.
	bdClose(t, bd, dir, step1.ID)

	// Pre-state: molecule root + epic step both still open.
	if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
		t.Fatalf("precondition: molecule root %s should be open before close-eligible, got %s", root.ID, got.Status)
	}
	if got := bdShow(t, bd, dir, epicStep.ID); got.Status != types.StatusOpen {
		t.Fatalf("precondition: epic step %s should be open (eligible) before close-eligible, got %s", epicStep.ID, got.Status)
	}

	// Close the eligible epic via `bd epic close-eligible`.
	bdEpic(t, bd, dir, "close-eligible")

	// The epic step itself must close (baseline close-eligible behavior).
	if got := bdShow(t, bd, dir, epicStep.ID); got.Status != types.StatusClosed {
		t.Fatalf("`bd epic close-eligible` did not close eligible epic %s (status=%s)", epicStep.ID, got.Status)
	}

	// THE FIX (beads-4v7eb): closing the molecule's final step (the epic) must
	// cascade-close the auto-closing molecule root, at parity with `bd close`.
	if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd epic close-eligible` closed the molecule's final step (epic %s) but did NOT auto-close the completed molecule (beads-4v7eb)", root.ID, got.Status, types.StatusClosed, epicStep.ID)
	}
}

// TestEpicCloseEligible_NonFinalStep_DoesNotAutoClose_4v7eb is the negative:
// when the closed epic is NOT the molecule's last open step, the cascade must
// NOT fire — the root stays open. Pins the fix to genuine completion (guards
// against an over-eager unconditional root close).
func TestEpicCloseEligible_NonFinalStep_DoesNotAutoClose_4v7eb(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ecn")

	root := bdCreate(t, bd, dir, "molecule root", "--type", "molecule")
	// Two steps stay open besides the epic: an eligible epic step + a plain
	// open task step. Closing the epic still leaves the task open → NOT complete.
	epicStep := bdCreate(t, bd, dir, "epic step", "--type", "epic")
	openTask := bdCreate(t, bd, dir, "still-open task step", "--type", "task")
	bdDep(t, bd, dir, "add", epicStep.ID, root.ID, "--type", "parent-child")
	bdDep(t, bd, dir, "add", openTask.ID, root.ID, "--type", "parent-child")

	epicChild := bdCreate(t, bd, dir, "epic child", "--type", "task")
	bdDep(t, bd, dir, "add", epicChild.ID, epicStep.ID, "--type", "parent-child")
	bdClose(t, bd, dir, epicChild.ID)

	bdEpic(t, bd, dir, "close-eligible")

	if got := bdShow(t, bd, dir, epicStep.ID); got.Status != types.StatusClosed {
		t.Fatalf("`bd epic close-eligible` did not close eligible epic %s (status=%s)", epicStep.ID, got.Status)
	}
	if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
		t.Errorf("molecule root %s status = %q, want %q — the molecule still has an open step (%s) so it must NOT auto-close (beads-4v7eb negative)", root.ID, got.Status, types.StatusOpen, openTask.ID)
	}
}

// TestProxiedEpicCloseEligible_AutoClosesCompletedMolecule_4v7eb is the PROXIED
// twin: a hub-connected (sql-server) crew running `bd epic close-eligible` goes
// through runEpicCloseEligibleProxiedServer, which closed each eligible epic via
// the UOW's CloseIssue but — like the direct path before this fix — dropped the
// molecule auto-close cascade. So superseding a molecule's final step (here, an
// eligible epic) via the proxied close-eligible left the root OPEN.
// MUTATION-VERIFIED: removing the autoCloseProxiedCompletedMolecule call in
// runEpicCloseEligibleProxiedServer's closeFn (or moving it after uw.Commit so
// the staged root-close is rolled back) leaves the root OPEN.
func TestProxiedEpicCloseEligible_AutoClosesCompletedMolecule_4v7eb(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pecm")

	root := bdProxiedCreate(t, bd, p.dir, "molecule root", "--type", "molecule")
	step1 := bdProxiedCreate(t, bd, p.dir, "step 1 task", "--type", "task")
	epicStep := bdProxiedCreate(t, bd, p.dir, "step 2 epic", "--type", "epic")
	for _, stepID := range []string{step1.ID, epicStep.ID} {
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", stepID, root.ID, "--type", "parent-child"); err != nil {
			t.Fatalf("proxied dep add %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
		}
	}
	epicChild := bdProxiedCreate(t, bd, p.dir, "epic child", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", epicChild.ID, epicStep.ID, "--type", "parent-child"); err != nil {
		t.Fatalf("proxied dep add epic child failed: %v\n%s", err, out)
	}
	if out, err := bdProxiedRun(t, bd, p.dir, "close", epicChild.ID, "--reason", "done"); err != nil {
		t.Fatalf("proxied close epic child failed: %v\n%s", err, out)
	}
	if out, err := bdProxiedRun(t, bd, p.dir, "close", step1.ID, "--reason", "done"); err != nil {
		t.Fatalf("proxied close step1 failed: %v\n%s", err, out)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "epic", "close-eligible"); err != nil {
		t.Fatalf("proxied `bd epic close-eligible` failed: %v\n%s", err, out)
	}

	if got := bdProxiedShow(t, bd, p.dir, epicStep.ID); got.Status != types.StatusClosed {
		t.Fatalf("proxied `bd epic close-eligible` did not close eligible epic %s (status=%s)", epicStep.ID, got.Status)
	}
	if got := bdProxiedShow(t, bd, p.dir, root.ID); got.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — proxied `bd epic close-eligible` closed the molecule's final step (epic %s) but did NOT auto-close the completed molecule (beads-4v7eb proxied)", root.ID, got.Status, types.StatusClosed, epicStep.ID)
	}
}
