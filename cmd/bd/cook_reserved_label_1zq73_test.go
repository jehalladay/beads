//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/formula"
)

// TestCookPersistRejectsReservedLabels_1zq73 pins the beads-1zq73 write-time
// reservation of the gt: identity family AND the 'provides:' capability family
// at the `bd cook --persist` authoring seam.
//
// A formula step's `labels:` flow verbatim (user/agent-authorable TOML) into
// plan.labels via collectStepIssue → collectSteps, and storage AddLabel has no
// guard (guards live in the cmd layer). Before the fix, a step carrying
// labels:["gt:role"] minted beads at RC=0 with a reserved identity label HIDDEN
// from `bd ready` (the 3c4g spoof foot-gun), and labels:["provides:cap"]
// bypassed `bd ship`'s CLOSED + single-provider invariants (the o70m1 vector).
// The guard lives at the shared cookPlanTx chokepoint (covers both the fresh
// cookFormula path and the --force delete-then-recreate path), mirroring the
// graph seam's validateGraphApplyPlan. cook --persist is atomic (transact), so
// a reject must create NOTHING.
//
// MUTATION-VERIFY: remove the reserved/provides loop at the top of cookPlanTx
// and the reject sub-tests go RED (cook succeeds, RC=0, proto minted).
func TestCookPersistRejectsReservedLabels_1zq73(t *testing.T) {
	ctx := context.Background()

	origStore := store
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origActor := actor
	t.Cleanup(func() {
		store = origStore
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		actor = origActor
	})
	rootCtx = ctx
	jsonOutput = false
	actor = "test-actor"

	// stepFormula builds a one-step formula whose single step carries the given
	// labels — the authoring vector under test.
	stepFormula := func(name string, labels ...string) *formula.Formula {
		return &formula.Formula{
			Formula:     name,
			Description: "1zq73 reserved-label fixture",
			Steps: []*formula.Step{
				{ID: "s1", Title: "step one", Type: "task", Labels: labels},
			},
		}
	}

	// freshStore returns an isolated real store swapped into the global for one
	// sub-case (each sub-case gets its own DB so a mint in one does not leak).
	freshStore := func(t *testing.T, prefix string) {
		t.Helper()
		real := newTestStore(t, filepath.Join(t.TempDir(), ".beads", "beads.db"))
		store = real
	}

	// protoMissing asserts the proto was NOT created (the atomic reject rolled
	// everything back).
	protoMissing := func(t *testing.T, protoID string) {
		t.Helper()
		got, err := store.GetIssue(ctx, protoID)
		if err == nil && got != nil {
			t.Errorf("proto %q was minted despite the reserved-label reject — the guard must create nothing", protoID)
		}
	}

	t.Run("gt_role_step_label_rejected_creates_nothing", func(t *testing.T) {
		t.Setenv("GT_INTERNAL", "") // ensure not a privileged gt-internal write
		freshStore(t, "cr")
		const protoID = "mol-1zq73-role"
		err := persistCookFormula(ctx, stepFormula(protoID, "gt:role"), protoID, false, nil, nil)
		if err == nil {
			t.Fatal("cook --persist with a gt:role step label should have failed, got nil")
		}
		if !strings.Contains(err.Error(), "reserved gt identity label") {
			t.Errorf("reject should name the reserved identity label, got: %v", err)
		}
		protoMissing(t, protoID)
	})

	t.Run("provides_step_label_rejected_creates_nothing", func(t *testing.T) {
		t.Setenv("GT_INTERNAL", "")
		freshStore(t, "cp")
		const protoID = "mol-1zq73-prov"
		err := persistCookFormula(ctx, stepFormula(protoID, "provides:mycap"), protoID, false, nil, nil)
		if err == nil {
			t.Fatal("cook --persist with a provides: step label should have failed, got nil")
		}
		if !strings.Contains(err.Error(), "provides:") || !strings.Contains(err.Error(), "bd ship") {
			t.Errorf("reject should mention 'provides:' and the 'bd ship' hint, got: %v", err)
		}
		protoMissing(t, protoID)
	})

	t.Run("ordinary_step_label_still_cooks", func(t *testing.T) {
		t.Setenv("GT_INTERNAL", "")
		freshStore(t, "co")
		const protoID = "mol-1zq73-ok"
		if err := persistCookFormula(ctx, stepFormula(protoID, "area:backend"), protoID, false, nil, nil); err != nil {
			t.Fatalf("cook --persist with an ordinary step label must still succeed, got: %v", err)
		}
		got, err := store.GetIssue(ctx, protoID)
		if err != nil || got == nil {
			t.Fatalf("expected the proto %q to be minted for an allowed label, got err=%v", protoID, err)
		}
	})

	t.Run("gt_role_allowed_under_gt_internal", func(t *testing.T) {
		// The GT_INTERNAL escape hatch: gt's own cooks (which set GT_INTERNAL=1)
		// must still stamp identity labels. providesLabelError is ungated, so this
		// only covers the identity family (a provides: step would still reject even
		// under GT_INTERNAL — that is intended, ship is the only path).
		t.Setenv("GT_INTERNAL", "1")
		freshStore(t, "ci")
		const protoID = "mol-1zq73-internal"
		if err := persistCookFormula(ctx, stepFormula(protoID, "gt:role"), protoID, false, nil, nil); err != nil {
			t.Fatalf("cook --persist with a gt:role label under GT_INTERNAL=1 must succeed (gt's own cooks), got: %v", err)
		}
		got, err := store.GetIssue(ctx, protoID)
		if err != nil || got == nil {
			t.Fatalf("expected the proto %q to be minted under GT_INTERNAL, got err=%v", protoID, err)
		}
	})
}
