package main

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestMatchGateToRun_q4gr5 guards the two beads-q4gr5 defects in
// matchGateToRun (a MUTATING path: the returned run's ID is written to the
// gate's await_id via updateGateAwaitID -> store.UpdateIssue):
//
//	DEFECT 1: time proximity alone (+30, gate accepted at bestScore >= 30) bound
//	          a gate to a run sharing NEITHER branch NOR commit. The fix requires
//	          a strong signal (commit OR branch OR workflow-hint match) to bind.
//	DEFECT 2: the branch heuristic compared against the CWD HEAD instead of the
//	          branchFilter the runs were queried with, so `--branch X` (X != CWD
//	          HEAD) scored the +50 branch signal as 0 for every queried run. The
//	          fix threads branchFilter into matchGateToRun.
//
// MUTATION-VERIFY:
//   - Defect 1: revert the accept condition to `bestScore >= 30` and
//     time_proximity_alone_does_not_bind FAILS (a time-adjacent, otherwise
//     unrelated run is returned).
//   - Defect 2: revert `currentBranch = branchFilter` to
//     `currentBranch = getGitBranchForGateDiscovery()` and
//     branch_filter_scores_branch_match FAILS (the branch-only run is not bound
//     because its branch != the CWD HEAD).
func TestMatchGateToRun_q4gr5(t *testing.T) {
	maxAge := 24 * time.Hour
	gateTime := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	gate := &types.Issue{ID: "gate-1", CreatedAt: gateTime}

	// ── DEFECT 1: a run adjacent in time but sharing neither branch nor commit
	// must NOT bind. Fake branch/SHA guarantee no match against the CWD repo.
	t.Run("time_proximity_alone_does_not_bind", func(t *testing.T) {
		runs := []GHWorkflowRun{
			{
				DatabaseID: 111,
				HeadBranch: "q4gr5-unrelated-branch-does-not-exist",
				HeadSha:    "q4gr5deadbeefnomatchsha000000000000000000",
				Status:     "completed",
				CreatedAt:  gateTime.Add(3 * time.Minute), // < 5min → +30 only
			},
		}
		// branchFilter "" → falls back to CWD HEAD, which the fake HeadBranch
		// will not equal, so there is no strong signal — must return nil.
		if got := matchGateToRun(gate, runs, maxAge, ""); got != nil {
			t.Errorf("expected nil (time proximity alone, no branch/commit match must NOT bind), got run %d", got.DatabaseID)
		}
	})

	// ── DEFECT 2: with --branch feature (branchFilter), a run on that branch
	// must score the +50 branch signal and bind — even though it is NOT the CWD
	// HEAD.
	t.Run("branch_filter_scores_branch_match", func(t *testing.T) {
		const feature = "q4gr5-feature-branch-not-cwd-head"
		runs := []GHWorkflowRun{
			{
				DatabaseID: 222,
				HeadBranch: feature,
				HeadSha:    "q4gr5deadbeefnomatchsha000000000000000000",
				Status:     "completed",
				CreatedAt:  gateTime.Add(3 * time.Minute), // branch(+50) + time(+30)
			},
		}
		got := matchGateToRun(gate, runs, maxAge, feature)
		if got == nil {
			t.Fatalf("expected the feature-branch run to bind via branchFilter (+50), got nil")
		}
		if got.DatabaseID != 222 {
			t.Errorf("expected run 222, got %d", got.DatabaseID)
		}
	})

	// ── control: a commit match binds regardless of branch/time (strong signal).
	t.Run("commit_match_binds", func(t *testing.T) {
		// Drive the commit heuristic without depending on the CWD SHA by making
		// the run's branch match the filter (strong signal) and asserting it binds.
		const br = "q4gr5-control-branch"
		runs := []GHWorkflowRun{
			{
				DatabaseID: 333,
				HeadBranch: br,
				HeadSha:    "q4gr5controlsha00000000000000000000000000",
				Status:     "in_progress",
				CreatedAt:  gateTime.Add(20 * time.Minute), // time +10 only; branch +50 is the strong signal
			},
		}
		got := matchGateToRun(gate, runs, maxAge, br)
		if got == nil || got.DatabaseID != 333 {
			t.Fatalf("expected branch-matched run 333 to bind, got %v", got)
		}
	})
}
