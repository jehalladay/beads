package dolt

import (
	"context"
	"database/sql"
	"testing"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// countInconsistencies wraps the read-only detection used by the bd doctor
// Blocked State check.
func countInconsistencies(ctx context.Context, t *testing.T, db *sql.DB) int64 {
	t.Helper()
	n, err := issueops.CountIsBlockedInconsistenciesInTx(ctx, db)
	if err != nil {
		t.Fatalf("CountIsBlockedInconsistenciesInTx: %v", err)
	}
	return n
}

// recomputeAll wraps the full repair used by 'bd doctor --fix' and returns the
// number of rows it corrected.
func recomputeAll(ctx context.Context, t *testing.T, db *sql.DB) int64 {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin recompute-all tx: %v", err)
	}
	changed, err := issueops.RecomputeAllIsBlockedInTx(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("RecomputeAllIsBlockedInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit recompute-all tx: %v", err)
	}
	return changed
}

// TestRecomputeAllIsBlocked_RepairsStaleClearedFlag is the bd-6dnrw.37 repair
// path: a row that SHOULD be blocked but whose is_blocked was left at 0 (the
// shape a skipped post-pull recompute leaves behind). Detection must see it,
// the full recompute must fix it, and detection and repair must then agree the
// database is consistent — the lockstep that keeps the COUNT predicate from
// drifting from the recompute SQL.
func TestRecomputeAllIsBlocked_RepairsStaleClearedFlag(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// Correct graph via the normal write path: bm-w blocked on open bm-x.
	seedBlockedPair(ctx, t, store, true)
	if !isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("precondition: bm-w should be blocked by open bm-x")
	}
	// A correctly-maintained graph has zero inconsistencies.
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("consistent graph: want 0 inconsistencies, got %d", n)
	}

	// Corrupt: clear bm-w's flag directly, with no recompute — exactly what a
	// merge that bypassed the recompute hook leaves behind.
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET is_blocked = 0 WHERE id = 'bm-w'"); err != nil {
		t.Fatalf("corrupt is_blocked: %v", err)
	}
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after corruption: want 1 inconsistency, got %d", n)
	}

	// Repair via the full recompute (the always-available path that does not
	// need a pull to advance HEAD).
	if changed := recomputeAll(ctx, t, store.db); changed != 1 {
		t.Fatalf("repair: want 1 row corrected, got %d", changed)
	}
	if !isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("after repair: bm-w must read blocked again")
	}
	// Detection and repair now agree, and the repair is idempotent.
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after repair: want 0 inconsistencies, got %d", n)
	}
	if again := recomputeAll(ctx, t, store.db); again != 0 {
		t.Fatalf("repair must be idempotent: want 0 on second run, got %d", again)
	}
}

// TestRecomputeAllIsBlocked_ClearsStuckBlockedFlag is the mirror case: a row
// left is_blocked = 1 after its only blocker was closed remotely (a merge that
// bypassed the recompute hook). `bd ready` would keep hiding it; the full
// recompute must clear the flag.
func TestRecomputeAllIsBlocked_ClearsStuckBlockedFlag(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	seedBlockedPair(ctx, t, store, true)
	if !isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("precondition: bm-w should be blocked by open bm-x")
	}

	// "Merge": the remote closed the blocker; no local recompute ran, so bm-w
	// is stuck is_blocked = 1 with a closed blocker.
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET status = 'closed' WHERE id = 'bm-x'"); err != nil {
		t.Fatalf("simulate merged close: %v", err)
	}
	if !isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("setup: bm-w must still read blocked before the recompute (the stale flag is the bug)")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after merged close: want 1 inconsistency, got %d", n)
	}

	if changed := recomputeAll(ctx, t, store.db); changed != 1 {
		t.Fatalf("repair: want 1 row corrected, got %d", changed)
	}
	if isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("after repair: bm-w must be unblocked (its only blocker is closed)")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after repair: want 0 inconsistencies, got %d", n)
	}
}

// TestRecomputeAllIsBlocked_CascadesThroughParentChild verifies the fixpoint:
// is_blocked propagates from a blocked parent to its child across passes. The
// single-pass detection COUNT is a documented lower bound here — it sees only
// the parent on the first pass — but the recompute corrects the whole chain and
// detection reaches 0 once it converges.
func TestRecomputeAllIsBlocked_CascadesThroughParentChild(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// bm-w blocked on open bm-x; bm-y is a child of bm-w, so bm-y inherits
	// blocked. All maintained by the normal write path.
	seedBlockedPair(ctx, t, store, true)
	child := &types.Issue{ID: "bm-y", Title: "bm-y", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, child, "tester"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{IssueID: "bm-y", DependsOnID: "bm-w", Type: types.DepParentChild}, "tester"); err != nil {
		t.Fatalf("add parent-child: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'seed parent-child chain')"); err != nil && !isDoltNothingToCommit(err) {
		t.Fatalf("commit chain: %v", err)
	}
	if !isBlocked(ctx, t, store.db, "bm-y") {
		t.Fatal("precondition: child bm-y should inherit blocked from parent bm-w")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("consistent chain: want 0 inconsistencies, got %d", n)
	}

	// Corrupt both the parent and the child to is_blocked = 0.
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET is_blocked = 0 WHERE id IN ('bm-w', 'bm-y')"); err != nil {
		t.Fatalf("corrupt chain: %v", err)
	}
	// Single-pass detection sees only the parent (the child's parent-child
	// reason depends on the parent's still-corrupted flag) — a lower bound.
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after corruption: want 1 (single-pass lower bound), got %d", n)
	}

	// The fixpoint corrects the whole chain across passes.
	if changed := recomputeAll(ctx, t, store.db); changed != 2 {
		t.Fatalf("repair: want 2 rows corrected across passes, got %d", changed)
	}
	if !isBlocked(ctx, t, store.db, "bm-w") || !isBlocked(ctx, t, store.db, "bm-y") {
		t.Fatal("after repair: both bm-w and bm-y must read blocked")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after repair: want 0 inconsistencies, got %d", n)
	}
}

// TestRecomputeAllIsBlocked_LockstepWaitsForGate (beads-hpmw) closes the
// test-gap where the lockstep invariant was only ever exercised on the
// parent-child and blocks branches of shouldBeBlockedDisjunction — never on the
// waits-for gate branch, which is the most drift-prone: the count predicate and
// the mark/unmark recompute templates share the ~35-line waitsForGateBlockedSQL
// constant by string interpolation, so a future edit that inlines it in one path
// but not the other would silently break lockstep with zero test signal. This
// seeds an ungated (any-children) waits-for spawner with an open child — the
// waiter SHOULD be blocked — then corrupts its flag and asserts the count
// detects it, the recompute fixes it, and the two agree at 0 (idempotent).
func TestRecomputeAllIsBlocked_LockstepWaitsForGate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// waiter waits-for spawner under an any-children gate; spawner has one open
	// child, so the gate is UNsatisfied and the waiter should be blocked. All
	// maintained by the normal write path (which recomputes is_blocked on add).
	createPerm(t, ctx, store, "hpmw-wf-waiter")
	createPerm(t, ctx, store, "hpmw-wf-spawner")
	createPerm(t, ctx, store, "hpmw-wf-child")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "hpmw-wf-child", DependsOnID: "hpmw-wf-spawner", Type: types.DepParentChild,
	}, "tester"); err != nil {
		t.Fatalf("seed parent-child: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "hpmw-wf-waiter", DependsOnID: "hpmw-wf-spawner",
		Type: types.DepWaitsFor, Metadata: `{"gate":"any-children"}`,
	}, "tester"); err != nil {
		t.Fatalf("seed waits-for any-children: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'seed waits-for gate')"); err != nil && !isDoltNothingToCommit(err) {
		t.Fatalf("commit seed: %v", err)
	}
	if !isBlocked(ctx, t, store.db, "hpmw-wf-waiter") {
		t.Fatal("precondition: waiter should be blocked (any-children gate unsatisfied, one open child)")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("consistent waits-for graph: want 0 inconsistencies, got %d", n)
	}

	// Corrupt: clear the waiter's flag with no recompute (the merge-bypass shape).
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET is_blocked = 0 WHERE id = 'hpmw-wf-waiter'"); err != nil {
		t.Fatalf("corrupt is_blocked: %v", err)
	}
	// This is the load-bearing assertion: the COUNT predicate's waits-for branch
	// (via waitsForGateBlockedSQL) must catch the stale flag. Deleting that
	// OR-branch from shouldBeBlockedDisjunction makes this go to 0 = RED (teeth).
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after corruption: want 1 inconsistency on the waits-for branch, got %d", n)
	}

	if changed := recomputeAll(ctx, t, store.db); changed != 1 {
		t.Fatalf("repair: want 1 row corrected, got %d", changed)
	}
	if !isBlocked(ctx, t, store.db, "hpmw-wf-waiter") {
		t.Fatal("after repair: waiter must read blocked again")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after repair: want 0 inconsistencies, got %d", n)
	}
	if again := recomputeAll(ctx, t, store.db); again != 0 {
		t.Fatalf("repair must be idempotent: want 0 on second run, got %d", again)
	}
}

// TestRecomputeAllIsBlocked_LockstepConditionalBlocks (beads-hpmw) closes the
// second gap: conditional-blocks is folded with 'blocks' in the SQL
// (d.type = 'blocks' OR d.type = 'conditional-blocks') across the count and both
// recompute templates, but was never seeded as a corruption case. A future edit
// that drops 'conditional-blocks' from ONE of the three templates (e.g. the
// unmark NOT EXISTS) would leave a conditional-blocks dependent stuck-blocked
// while every existing test stayed green. This seeds a conditional-blocks dep on
// an OPEN blocker (the dependent should be blocked), corrupts the flag, and
// asserts count/recompute lockstep.
func TestRecomputeAllIsBlocked_LockstepConditionalBlocks(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "hpmw-cb-dependent")
	createPerm(t, ctx, store, "hpmw-cb-blocker")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "hpmw-cb-dependent", DependsOnID: "hpmw-cb-blocker", Type: types.DepConditionalBlocks,
	}, "tester"); err != nil {
		t.Fatalf("seed conditional-blocks: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'seed conditional-blocks')"); err != nil && !isDoltNothingToCommit(err) {
		t.Fatalf("commit seed: %v", err)
	}
	if !isBlocked(ctx, t, store.db, "hpmw-cb-dependent") {
		t.Fatal("precondition: dependent should be blocked by open conditional-blocks blocker")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("consistent conditional-blocks graph: want 0 inconsistencies, got %d", n)
	}

	// Corrupt the mark side: clear the flag on a dependent that should be blocked.
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET is_blocked = 0 WHERE id = 'hpmw-cb-dependent'"); err != nil {
		t.Fatalf("corrupt is_blocked: %v", err)
	}
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after mark-side corruption: want 1 inconsistency, got %d", n)
	}
	if changed := recomputeAll(ctx, t, store.db); changed != 1 {
		t.Fatalf("mark-side repair: want 1 row corrected, got %d", changed)
	}
	if !isBlocked(ctx, t, store.db, "hpmw-cb-dependent") {
		t.Fatal("after mark-side repair: dependent must read blocked again")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after mark-side repair: want 0 inconsistencies, got %d", n)
	}

	// Now the unmark side: close the blocker "remotely" (raw SQL, no write path),
	// leaving the dependent stuck is_blocked = 1 with no live conditional-blocks
	// reason. The count's unmark branch must catch it and the recompute clear it.
	// This is the assertion that guards conditional-blocks in the unmark template.
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET status = 'closed' WHERE id = 'hpmw-cb-blocker'"); err != nil {
		t.Fatalf("simulate merged close of blocker: %v", err)
	}
	if !isBlocked(ctx, t, store.db, "hpmw-cb-dependent") {
		t.Fatal("setup: dependent must still read blocked before recompute (the stale flag is the bug)")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after merged close: want 1 unmark-eligible inconsistency, got %d", n)
	}
	if changed := recomputeAll(ctx, t, store.db); changed != 1 {
		t.Fatalf("unmark-side repair: want 1 row corrected, got %d", changed)
	}
	if isBlocked(ctx, t, store.db, "hpmw-cb-dependent") {
		t.Fatal("after unmark-side repair: dependent must be unblocked (blocker closed)")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after unmark-side repair: want 0 inconsistencies, got %d", n)
	}
	if again := recomputeAll(ctx, t, store.db); again != 0 {
		t.Fatalf("repair must be idempotent: want 0 on second run, got %d", again)
	}
}
