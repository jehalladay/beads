package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestSwarmStatusExternalBlocker_k0ln4 is the teeth for beads-k0ln4: the swarm
// STATUS categorization path (getSwarmStatus) had the SAME outside-epic blind
// spot as the wave path (beads-r7sh3). getEpicChildren returns only the epic's
// DIRECT children, so getSwarmStatus's dependsOn map (built with a childIDSet
// filter) dropped any blocking edge to a NON-child issue — a child blocked only
// by an OPEN outside-epic issue got empty blockers and was mis-categorized as
// status.Ready, inflating ReadyCount, while `bd ready` (whose is_blocked
// predicate is epic-agnostic) EXCLUDES it. The fix resolves the external
// blocker's activeness via issueops.IsActiveBlockerByState (the Go mirror of
// activeBlockerSQL) and folds it into the child's blockers → categorized
// Blocked with the external ID in BlockedBy, matching bd ready.
//
// MUTATION-VERIFY: drop the externalBlockers append in the categorize loop (or
// the externalBlockers-map build) and the "open outside-epic blocker →
// Blocked" subtest FAILS — the child re-appears in status.Ready.
func TestSwarmStatusExternalBlocker_k0ln4(t *testing.T) {
	ctx := context.Background()
	epic := &types.Issue{ID: "epic", Title: "Epic"}

	inCategory := func(cat []StatusIssue, id string) *StatusIssue {
		for i := range cat {
			if cat[i].ID == id {
				return &cat[i]
			}
		}
		return nil
	}

	t.Run("OPEN outside-epic blocker categorizes the child Blocked (matches bd ready)", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "c", Title: "c", Status: types.StatusOpen}}
		f.depRecords["c"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "ext", Type: types.DepBlocks},
		}
		// 'ext' is NOT a child of the epic and is OPEN → an active 'blocks' blocker.
		f.issues["ext"] = &types.Issue{ID: "ext", Title: "ext", Status: types.StatusOpen}

		st, err := getSwarmStatus(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if inCategory(st.Ready, "c") != nil {
			t.Errorf("child blocked by an open outside-epic issue must NOT be Ready (bd ready excludes it)")
		}
		bc := inCategory(st.Blocked, "c")
		if bc == nil {
			t.Fatalf("child blocked by an open outside-epic issue must be categorized Blocked")
		}
		found := false
		for _, b := range bc.BlockedBy {
			if b == "ext" {
				found = true
			}
		}
		if !found {
			t.Errorf("BlockedBy should list the external blocker 'ext', got %v", bc.BlockedBy)
		}
		if st.ReadyCount != 0 {
			t.Errorf("ReadyCount should exclude the externally-blocked child, got %d", st.ReadyCount)
		}
		if st.BlockedCount != 1 {
			t.Errorf("BlockedCount should count the externally-blocked child, got %d", st.BlockedCount)
		}
	})

	t.Run("CLOSED outside-epic blocker leaves the child Ready (bd ready surfaces it)", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "c", Title: "c", Status: types.StatusOpen}}
		f.depRecords["c"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "ext", Type: types.DepBlocks},
		}
		// A 'blocks' edge is satisfied by ANY close.
		f.issues["ext"] = &types.Issue{ID: "ext", Title: "ext", Status: types.StatusClosed}

		st, err := getSwarmStatus(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if inCategory(st.Blocked, "c") != nil {
			t.Errorf("child whose external 'blocks' blocker is CLOSED must NOT be Blocked")
		}
		if inCategory(st.Ready, "c") == nil {
			t.Errorf("child with a satisfied (closed) external blocker should be Ready")
		}
	})

	t.Run("conditional-blocks success-closed outside-epic blocker still blocks (matches bd ready)", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "c", Title: "c", Status: types.StatusOpen}}
		f.depRecords["c"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "ext", Type: types.DepConditionalBlocks},
		}
		// conditional-blocks: a SUCCESS close leaves "runs only if it fails" unmet
		// → still an active blocker (IsActiveBlockerByState=true).
		f.issues["ext"] = &types.Issue{ID: "ext", Title: "ext", Status: types.StatusClosed, CloseReason: "done"}

		st, err := getSwarmStatus(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if inCategory(st.Ready, "c") != nil {
			t.Errorf("child with a success-closed conditional-blocks external blocker must NOT be Ready")
		}
		if inCategory(st.Blocked, "c") == nil {
			t.Errorf("child with a success-closed conditional-blocks external blocker must stay Blocked")
		}
	})

	t.Run("cross-project external: ref is warning-only, leaves the child Ready (matches bd ready EXISTS-false)", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "c", Title: "c", Status: types.StatusOpen}}
		f.depRecords["c"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "external:OTHER-1", Type: types.DepBlocks},
		}
		// No local row for the external: ref → bd ready's is_blocked JOIN finds
		// nothing → not blocking.
		st, err := getSwarmStatus(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if inCategory(st.Blocked, "c") != nil {
			t.Errorf("a cross-project external: ref must NOT categorize the child Blocked (bd ready EXISTS-false)")
		}
		if inCategory(st.Ready, "c") == nil {
			t.Errorf("child with only a cross-project external: ref should be Ready")
		}
	})
}
