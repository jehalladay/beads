package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-8tw1a: `bd mol distill` silently drops dependency edges whose target is
// OUTSIDE the distilled epic (cross-epic-boundary). subgraphToFormula only
// carries a depends_on when the target is another child of the same epic
// (idToStepID hit); a dep on an external issue X is skipped with no warning and
// no change to the step count, so a poured molecule silently loses a blocker the
// source epic had. Dropping is INTENDED (formulas must be self-contained) — the
// SILENCE is the bug. The fix surfaces the dropped edges (externalDepDrops) and
// warns (warnDroppedExternalDeps), mirroring the orphan-var warning class. This
// test builds the bead-comment repro: epic E, child C blocks-depends external X,
// child C also depends on in-epic child D.
//
// MUTATION-VERIFY: make externalDepDrops range over an empty slice (e.g. add
// `return nil` at the top) → it reports zero drops → the "reports exactly the
// external edge" and "warning names X" asserts FAIL. The in-epic-preserved
// assert on subgraphToFormula is an independent control that must stay green
// either way (proves we did not change the drop).
//
// IMPORTANT (the original 8tw1a false-green): this subgraph mirrors what
// loadTemplateSubgraph actually produces — the cross-boundary edge C→X lives in
// ExternalDeps, NOT Dependencies (whose invariant is both-ends-in-subgraph). The
// prior test placed C→X in Dependencies, an UNREPRESENTATIVE shape the live
// loader never yields, so the passing test hid the fact that externalDepDrops
// (which scanned Dependencies) could never see a real drop. The embedded teeth
// in mol_distill_external_dep_drop_8tw1a_embedded_test.go drive the real
// runMolDistill / loadTemplateSubgraph path end-to-end as the true guard.
func TestDistillWarnsOnDroppedExternalDep_8tw1a(t *testing.T) {
	// Epic E with two children C and D. C depends on D (in-epic) AND on X
	// (external — not among E's Issues). subgraphToFormula must preserve C→D as
	// a step depends_on and drop C→X; externalDepDrops must report exactly C→X.
	rootID, cID, dID, extID := "E", "C", "D", "X"
	subgraph := &TemplateSubgraph{
		Root: &types.Issue{ID: rootID, Title: "Deploy service", Description: "deploy epic"},
		Issues: []*types.Issue{
			{ID: rootID, Title: "Deploy service", Description: "deploy epic"},
			{ID: cID, Title: "run migration", Description: "apply schema"},
			{ID: dID, Title: "provision db", Description: "spin up"},
		},
		Dependencies: []*types.Dependency{
			// C depends on in-epic sibling D (must be preserved as a step dep).
			{IssueID: cID, DependsOnID: dID, Type: types.DepBlocks},
		},
		ExternalDeps: []*types.Dependency{
			// C depends on EXTERNAL issue X (must be dropped + warned) — this is
			// where loadTemplateSubgraph records a cross-boundary edge.
			{IssueID: cID, DependsOnID: extID, Type: types.DepBlocks},
		},
	}

	f := subgraphToFormula(subgraph, "deploy", map[string]string{})
	if f == nil {
		t.Fatal("subgraphToFormula returned nil")
	}

	// Control (independent of the fix): the in-epic dep C→D is preserved as a
	// step depends_on, and the external dep C→X is NOT — the drop is unchanged.
	dStepID := sanitizeFormulaName("provision db")
	var stepC *stepView
	for _, s := range f.Steps {
		if s.ID == sanitizeFormulaName("run migration") {
			stepC = &stepView{id: s.ID, deps: s.DependsOn}
		}
	}
	if stepC == nil {
		t.Fatalf("step for child C not found in emitted formula; steps=%+v", f.Steps)
	}
	if !depsContain(stepC.deps, dStepID) {
		t.Errorf("in-epic dep C→D must be preserved as a step depends_on %q; got %v", dStepID, stepC.deps)
	}
	for _, d := range stepC.deps {
		if d == extID || d == sanitizeFormulaName(extID) {
			t.Errorf("external dep C→X should be dropped from step depends_on (drop is intended); got %v", stepC.deps)
		}
	}

	// The fix: externalDepDrops reports exactly the cross-boundary edge C→X and
	// nothing else (not the in-epic C→D, not any root edge).
	drops := externalDepDrops(subgraph)
	if len(drops) != 1 {
		t.Fatalf("expected exactly 1 dropped external dep (C→X); got %d: %+v", len(drops), drops)
	}
	if drops[0].FromID != cID || drops[0].TargetID != extID {
		t.Errorf("dropped edge should be C→X; got %s→%s", drops[0].FromID, drops[0].TargetID)
	}

	// The warning must name the external target X and the source child C.
	var buf bytes.Buffer
	warnDroppedExternalDeps(&buf, drops)
	out := buf.String()
	if !strings.Contains(out, extID) {
		t.Errorf("warning must name the dropped external target %q; got:\n%s", extID, out)
	}
	if !strings.Contains(out, cID) {
		t.Errorf("warning must name the source child %q whose dep was dropped; got:\n%s", cID, out)
	}
	if !strings.Contains(strings.ToLower(out), "warning") {
		t.Errorf("dropped-dep message should be a warning; got:\n%s", out)
	}
}

// TestDistillNoWarnWhenAllDepsInEpic_8tw1a is the negative control: a fully
// self-contained epic (every dep target is an in-epic child or the root) drops
// nothing, so externalDepDrops is empty and no warning is emitted. This guards
// against a false-positive warning on the common case.
func TestDistillNoWarnWhenAllDepsInEpic_8tw1a(t *testing.T) {
	rootID, cID, dID := "E", "C", "D"
	subgraph := &TemplateSubgraph{
		Root: &types.Issue{ID: rootID, Title: "Deploy service", Description: "deploy epic"},
		Issues: []*types.Issue{
			{ID: rootID, Title: "Deploy service", Description: "deploy epic"},
			{ID: cID, Title: "run migration", Description: "apply schema"},
			{ID: dID, Title: "provision db", Description: "spin up"},
		},
		Dependencies: []*types.Dependency{
			{IssueID: cID, DependsOnID: dID, Type: types.DepBlocks},    // in-epic
			{IssueID: cID, DependsOnID: rootID, Type: types.DepBlocks}, // on root (intentionally elided, not a silent loss)
		},
	}

	drops := externalDepDrops(subgraph)
	if len(drops) != 0 {
		t.Errorf("a self-contained epic (in-epic + root deps only) must drop nothing; got %+v", drops)
	}

	var buf bytes.Buffer
	if len(drops) > 0 {
		warnDroppedExternalDeps(&buf, drops)
	}
	if buf.Len() != 0 {
		t.Errorf("no warning should be emitted when nothing is dropped; got:\n%s", buf.String())
	}
}

type stepView struct {
	id   string
	deps []string
}

func depsContain(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
