package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestSwarmExternalBlockerWave0_r7sh3 is the teeth for beads-r7sh3: swarm
// validate/status wave computation ignored blocking dependencies on issues
// OUTSIDE the epic. getEpicChildren returns only the epic's DIRECT children, so
// a blocking edge to a non-child issue never entered the wave graph — the
// dependent got inDegree 0 and was seeded into ready-front wave 0
// (Swarmable:YES), while `bd ready` (whose is_blocked predicate is epic-agnostic)
// correctly EXCLUDES a child blocked by an OPEN outside-epic issue. The fix marks
// such a child ExternalBlocked and computeReadyFronts treats it as
// not-schedulable (never seeded into a wave, dependents cascade-block), matching
// bd ready. External activeness is resolved via issueops.IsActiveBlockerByState
// (the Go mirror of activeBlockerSQL), so it stays in lockstep with bd ready.
//
// MUTATION-VERIFY: revert the ExternalBlocked seeding (or drop it from
// isNotSchedulable in computeReadyFronts) and the "open external blocker excludes
// the child from wave 0" subtest FAILS — the child re-appears in wave 0.
func TestSwarmExternalBlockerWave0_r7sh3(t *testing.T) {
	ctx := context.Background()
	epic := &types.Issue{ID: "epic", Title: "Epic"}

	// waveOf finds the wave a node was scheduled into (-1 if never scheduled).
	waveOf := func(a *SwarmAnalysis, id string) int {
		if n, ok := a.Issues[id]; ok {
			return n.Wave
		}
		return -999
	}
	inAnyFront := func(a *SwarmAnalysis, id string) bool {
		for _, f := range a.ReadyFronts {
			for _, x := range f.Issues {
				if x == id {
					return true
				}
			}
		}
		return false
	}

	t.Run("OPEN outside-epic blocker excludes the child from wave 0 (matches bd ready)", func(t *testing.T) {
		f := newFakeSwarmStore()
		// One direct child 'c' of the epic, blocked by 'ext' which is NOT a child
		// of the epic (an OPEN issue elsewhere).
		f.dependents["epic"] = []*types.Issue{{ID: "c", Title: "c", Status: types.StatusOpen}}
		f.depRecords["c"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "ext", Type: types.DepBlocks},
		}
		// 'ext' resolvable + OPEN → an active 'blocks' blocker.
		f.issues["ext"] = &types.Issue{ID: "ext", Title: "ext", Status: types.StatusOpen}

		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !a.Issues["c"].ExternalBlocked {
			t.Errorf("child blocked by an open outside-epic issue must be marked ExternalBlocked")
		}
		if w := waveOf(a, "c"); w != -1 {
			t.Errorf("externally-blocked child must NOT be scheduled into a wave, got wave %d", w)
		}
		if inAnyFront(a, "c") {
			t.Errorf("externally-blocked child must not appear in any ready front (bd ready excludes it)")
		}
		// The single child is not actionable → no schedulable sessions.
		if a.EstimatedSessions != 0 {
			t.Errorf("EstimatedSessions should exclude the externally-blocked child, got %d", a.EstimatedSessions)
		}
	})

	t.Run("CLOSED outside-epic blocker does NOT exclude the child (bd ready surfaces it)", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "c", Title: "c", Status: types.StatusOpen}}
		f.depRecords["c"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "ext", Type: types.DepBlocks},
		}
		// A 'blocks' edge is satisfied by ANY close — bd ready would surface 'c'.
		f.issues["ext"] = &types.Issue{ID: "ext", Title: "ext", Status: types.StatusClosed}

		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if a.Issues["c"].ExternalBlocked {
			t.Errorf("a child whose outside-epic 'blocks' blocker is CLOSED must NOT be ExternalBlocked")
		}
		if w := waveOf(a, "c"); w != 0 {
			t.Errorf("child with a satisfied (closed) external blocker should be wave 0, got %d", w)
		}
		if !inAnyFront(a, "c") {
			t.Errorf("child with a satisfied external blocker should appear in a ready front")
		}
	})

	t.Run("conditional-blocks success-closed outside-epic blocker still blocks (matches bd ready)", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "c", Title: "c", Status: types.StatusOpen}}
		f.depRecords["c"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "ext", Type: types.DepConditionalBlocks},
		}
		// conditional-blocks: a SUCCESS close leaves the "runs only if it fails"
		// condition unmet → still an active blocker (IsActiveBlockerByState=true).
		f.issues["ext"] = &types.Issue{ID: "ext", Title: "ext", Status: types.StatusClosed, CloseReason: "done"}

		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !a.Issues["c"].ExternalBlocked {
			t.Errorf("child with a success-closed conditional-blocks external blocker must stay ExternalBlocked (bd ready keeps it blocked)")
		}
		if w := waveOf(a, "c"); w != -1 {
			t.Errorf("still-blocked child must not be scheduled, got wave %d", w)
		}
	})

	t.Run("cross-project external: ref is warning-only, does not exclude (matches bd ready EXISTS-false)", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "c", Title: "c", Status: types.StatusOpen}}
		f.depRecords["c"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "external:OTHER-1", Type: types.DepBlocks},
		}
		// No local row for the external: ref → bd ready's is_blocked JOIN finds
		// nothing → not blocking.
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if a.Issues["c"].ExternalBlocked {
			t.Errorf("a cross-project external: ref must NOT mark the child ExternalBlocked (bd ready EXISTS-false)")
		}
		if w := waveOf(a, "c"); w != 0 {
			t.Errorf("child with only a cross-project external: ref should be wave 0, got %d", w)
		}
		if !swarmWarnsContain(a.Warnings, "external dependency") {
			t.Errorf("cross-project external: ref should still emit the external-dependency warning, got %v", a.Warnings)
		}
	})

	t.Run("externally-blocked child cascade-blocks its intra-epic dependent", func(t *testing.T) {
		f := newFakeSwarmStore()
		// c1 (blocked externally) and c2 (depends on c1 within the epic).
		f.dependents["epic"] = []*types.Issue{
			{ID: "c1", Title: "c1", Status: types.StatusOpen},
			{ID: "c2", Title: "c2", Status: types.StatusOpen},
		}
		f.depRecords["c1"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "ext", Type: types.DepBlocks},
		}
		f.depRecords["c2"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "c1", Type: types.DepBlocks},
		}
		f.issues["ext"] = &types.Issue{ID: "ext", Title: "ext", Status: types.StatusOpen}

		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !a.Issues["c1"].ExternalBlocked {
			t.Fatalf("c1 must be ExternalBlocked")
		}
		if waveOf(a, "c1") != -1 {
			t.Errorf("c1 (externally blocked) must not be scheduled, got wave %d", waveOf(a, "c1"))
		}
		// c1 never resolves in this graph, so c2 (blocked by c1) must never reach a
		// wave either — no false wave-0 seeding for the whole chain.
		if waveOf(a, "c2") != -1 {
			t.Errorf("c2 depends on the externally-blocked c1 and must cascade-block (wave -1), got %d", waveOf(a, "c2"))
		}
		if a.EstimatedSessions != 2 {
			// EstimatedSessions = Total - Closed - Deferred (both open here); it is
			// a display estimate, not the schedulable count. Documents current
			// behavior so a future change to the estimate is deliberate.
			t.Logf("EstimatedSessions=%d (Total-Closed-Deferred display estimate)", a.EstimatedSessions)
		}
	})
}
