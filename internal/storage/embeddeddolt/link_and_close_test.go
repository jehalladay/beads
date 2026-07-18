//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestLinkAndClose is the atomicity guard for beads-njnw: bd duplicate /
// bd supersede must add the dependency edge AND close the source issue in ONE
// transaction. Before the fix they ran as two separate committed txns, so a
// failure between them left the edge added while the issue stayed open — a
// recoverable-but-inconsistent split state (same class as compaction's
// overwrite+mark, beads-pj38).
func TestLinkAndClose(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	// ── success: edge added AND source closed together ──────────────────────
	t.Run("links_and_closes_atomically", func(t *testing.T) {
		te := newTestEnv(t, "lc")
		ctx := t.Context()

		dup := &types.Issue{ID: "lc-dup", Title: "Duplicate", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		canon := &types.Issue{ID: "lc-canon", Title: "Canonical", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, dup, "tester"); err != nil {
			t.Fatalf("CreateIssue dup: %v", err)
		}
		if err := te.store.CreateIssue(ctx, canon, "tester"); err != nil {
			t.Fatalf("CreateIssue canon: %v", err)
		}

		dep := &types.Dependency{IssueID: "lc-dup", DependsOnID: "lc-canon", Type: types.DepDuplicates}
		if err := te.store.LinkAndClose(ctx, dep, "tester"); err != nil {
			t.Fatalf("LinkAndClose: %v", err)
		}

		// The edge exists.
		var edges int
		te.queryScalar(t, ctx,
			"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND depends_on_issue_id = ? AND type = ?",
			[]any{"lc-dup", "lc-canon", string(types.DepDuplicates)}, &edges)
		if edges != 1 {
			t.Errorf("expected 1 duplicates edge, got %d", edges)
		}
		// The source is closed.
		var status string
		te.queryScalar(t, ctx, "SELECT status FROM issues WHERE id = ?", []any{"lc-dup"}, &status)
		if status != string(types.StatusClosed) {
			t.Errorf("expected lc-dup status=closed, got %q", status)
		}
	})

	// ── failure: nonexistent target → NEITHER edge nor close applied ─────────
	t.Run("nonexistent_target_no_partial_write", func(t *testing.T) {
		te := newTestEnv(t, "lcx")
		ctx := t.Context()

		dup := &types.Issue{ID: "lcx-dup", Title: "Duplicate", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, dup, "tester"); err != nil {
			t.Fatalf("CreateIssue dup: %v", err)
		}

		dep := &types.Dependency{IssueID: "lcx-dup", DependsOnID: "lcx-nope999", Type: types.DepDuplicates}
		if err := te.store.LinkAndClose(ctx, dep, "tester"); err == nil {
			t.Fatalf("expected LinkAndClose to fail on nonexistent target")
		}

		var edges int
		te.queryScalar(t, ctx, "SELECT COUNT(*) FROM dependencies WHERE issue_id = ?", []any{"lcx-dup"}, &edges)
		if edges != 0 {
			t.Errorf("expected 0 edges after failed link, got %d", edges)
		}
		var status string
		te.queryScalar(t, ctx, "SELECT status FROM issues WHERE id = ?", []any{"lcx-dup"}, &status)
		if status != string(types.StatusOpen) {
			t.Errorf("expected lcx-dup to stay open after failed link, got %q", status)
		}
	})

	// ── rollback: edge-add succeeds but the close leg fails → edge rolled back.
	// This is the exact split-state njnw fixes. We force the close to fail by
	// corrupting the source's persisted priority out of the 0-4 range first; the
	// close leg's finalize-validation (ValidateWithCustom) then rejects the
	// merged issue, so the whole tx — including the already-inserted edge —
	// rolls back.
	t.Run("close_failure_rolls_back_edge", func(t *testing.T) {
		te := newTestEnv(t, "lcr")
		ctx := t.Context()

		dup := &types.Issue{ID: "lcr-dup", Title: "Duplicate", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		canon := &types.Issue{ID: "lcr-canon", Title: "Canonical", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, dup, "tester"); err != nil {
			t.Fatalf("CreateIssue dup: %v", err)
		}
		if err := te.store.CreateIssue(ctx, canon, "tester"); err != nil {
			t.Fatalf("CreateIssue canon: %v", err)
		}

		// Poison the source's priority so the close leg's finalize-validation fails.
		te.exec(t, ctx, "UPDATE issues SET priority = 99 WHERE id = ?", "lcr-dup")

		dep := &types.Dependency{IssueID: "lcr-dup", DependsOnID: "lcr-canon", Type: types.DepDuplicates}
		if err := te.store.LinkAndClose(ctx, dep, "tester"); err == nil {
			t.Fatalf("expected LinkAndClose to fail when the close leg violates invariants")
		}

		// The edge must NOT survive — it was added earlier in the same tx that
		// then failed at the close. A non-atomic implementation would leave it.
		var edges int
		te.queryScalar(t, ctx,
			"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND depends_on_issue_id = ?",
			[]any{"lcr-dup", "lcr-canon"}, &edges)
		if edges != 0 {
			t.Errorf("expected the edge to roll back with the failed close, got %d edges (split state!)", edges)
		}
	})
}
