//go:build cgo

package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-k2fwc: `bd ado sync` reconciliation closes a local issue whose ADO work
// item was deleted (404) via store.CloseIssue directly (closeReconciledDeletedIssues,
// ado.go). It already carries the GC-survivable audit-file-trail leg (beads-sxgz3
// / n4sn), but — like `bd update --status closed` (beads-zzp26), `bd duplicate`/
// supersede (beads-26gea), and `bd duplicates --auto-merge` (beads-z252q) — it
// bypassed the cmd-layer completed-molecule auto-close cascade `bd close` runs
// (autoCloseCompletedMolecule, close.go:223). So reconcile-closing a molecule/
// wisp/template-epic's FINAL open step left the auto-closing root stuck OPEN
// (orphaned-completed-root), the exact class z252q just fixed for the
// duplicates.go / performMerge leg and 26gea for duplicate.go.
//
// closeReconciledDeletedIssues is only reachable from `bd ado sync`, which needs
// a live ADO server, so — like the sxgz3 audit-trail teeth — this drives the
// extracted function directly against an embedded store (no mock ADO harness),
// seeding the molecule via the SAME store handle for read-your-own-write
// coherence. Uses the embedded-dolt harness (bdInit + openStore), no Docker.
//
// MUTATION-VERIFIED: removing the autoCloseCompletedMolecule call in
// closeReconciledDeletedIssues leaves the root OPEN → this test goes RED.
func TestADOReconcileClose_AutoClosesCompletedMolecule_k2fwc(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "ado")

	st := openStore(t, beadsDir, "ado")
	ctx := context.Background()
	const actor = "test-actor"

	// A molecule whose only OPEN step is the final one, mapped to a (now-deleted)
	// ADO work item #4242. Seeded via the same store handle used to close it.
	const rootID = "ado-1"
	const finalStep = "ado-2"
	seedMoleculeRootK2fwc(t, ctx, st, rootID, actor)
	seedStepUnderRootK2fwc(t, ctx, st, finalStep, rootID, actor)

	// The audit FILE resolves relative to cwd (.beads/interactions.jsonl), so
	// chdir into the workspace before the close path — same as sxgz3 / the
	// autoclose family tests.
	chdirForK2fwc(t, dir)

	adoIDMap := map[int]string{4242: finalStep}
	var warnings []string
	var out bytes.Buffer
	closeReconciledDeletedIssues(ctx, st, actor, []string{"4242"}, adoIDMap, &out, &warnings)

	// Precondition: the reconciled step actually closed (DB side).
	if step := mustGetIssueK2fwc(t, ctx, st, finalStep); step.Status != types.StatusClosed {
		t.Fatalf("reconcile did not close final step %s (status=%q) — close precondition broken", finalStep, step.Status)
	}

	// The molecule root must have auto-closed: its last open step was closed.
	if root := mustGetIssueK2fwc(t, ctx, st, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — reconcile-closing the final step did not auto-close the completed molecule (beads-k2fwc)",
			rootID, root.Status, types.StatusClosed)
	}
}

// Negative (no false positive): reconcile-closing a step that is NOT the
// molecule's final open step must NOT auto-close the root — the molecule is
// still incomplete. Guards against the cascade firing on partial completion.
func TestADOReconcileClose_NonFinalStepDoesNotAutoCloseRoot_k2fwc(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "adn")

	st := openStore(t, beadsDir, "adn")
	ctx := context.Background()
	const actor = "test-actor"

	// Molecule with TWO open steps; reconcile-close only ONE.
	const rootID = "adn-1"
	const step1 = "adn-2"
	const step2 = "adn-3"
	seedMoleculeRootK2fwc(t, ctx, st, rootID, actor)
	seedStepUnderRootK2fwc(t, ctx, st, step1, rootID, actor)
	seedStepUnderRootK2fwc(t, ctx, st, step2, rootID, actor)

	chdirForK2fwc(t, dir)

	adoIDMap := map[int]string{4242: step1}
	var warnings []string
	var out bytes.Buffer
	closeReconciledDeletedIssues(ctx, st, actor, []string{"4242"}, adoIDMap, &out, &warnings)

	if step := mustGetIssueK2fwc(t, ctx, st, step1); step.Status != types.StatusClosed {
		t.Fatalf("precondition: step1 %s was not closed (status=%q)", step1, step.Status)
	}
	if root := mustGetIssueK2fwc(t, ctx, st, rootID); root.Status == types.StatusClosed {
		t.Errorf("molecule root %s auto-closed after reconcile-closing only ONE of two steps — the cascade must fire only on real completion (beads-k2fwc)", rootID)
	}
}

func seedMoleculeRootK2fwc(t *testing.T, ctx context.Context, st storage.DoltStorage, rootID, actor string) {
	t.Helper()
	if err := st.CreateIssue(ctx, &types.Issue{
		ID:        rootID,
		Title:     "molecule root",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeMolecule,
	}, actor); err != nil {
		t.Fatalf("seed molecule root %s: %v", rootID, err)
	}
}

func seedStepUnderRootK2fwc(t *testing.T, ctx context.Context, st storage.DoltStorage, stepID, rootID, actor string) {
	t.Helper()
	if err := st.CreateIssue(ctx, &types.Issue{
		ID:        stepID,
		Title:     "step " + stepID,
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}, actor); err != nil {
		t.Fatalf("seed step %s: %v", stepID, err)
	}
	if err := st.AddDependency(ctx, &types.Dependency{
		IssueID:     stepID,
		DependsOnID: rootID,
		Type:        types.DepParentChild,
	}, actor); err != nil {
		t.Fatalf("link step %s -> root %s (parent-child): %v", stepID, rootID, err)
	}
}

func mustGetIssueK2fwc(t *testing.T, ctx context.Context, st storage.DoltStorage, id string) *types.Issue {
	t.Helper()
	issue, err := st.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("GetIssue(%s): %v", id, err)
	}
	if issue == nil {
		t.Fatalf("GetIssue(%s): nil", id)
	}
	return issue
}

// chdirForK2fwc changes into dir for the test's duration (audit file is cwd-relative).
func chdirForK2fwc(t *testing.T, dir string) {
	t.Helper()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
}
