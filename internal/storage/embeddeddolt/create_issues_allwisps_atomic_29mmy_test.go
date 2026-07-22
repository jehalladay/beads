//go:build cgo

package embeddeddolt_test

import (
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// TestCreateIssuesAllWispsBatchAtomic is the atomicity guard for beads-29mmy:
// the EmbeddedDoltStore all-wisps fast path must create every wisp in the batch
// in ONE transaction. Before the fix it looped calling withConn(ctx, true, ...)
// once per wisp — an implicit commit per iteration — so a failure on wisp k left
// wisps 0..k-1 committed, violating the single-transaction CreateIssues contract
// that the server DoltStore path already honors (one withRetryTx wrapping the
// whole batch). This is the batch member of the multi-write-atomicity family.
func TestCreateIssuesAllWispsBatchAtomic(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	// ── success: every wisp in an all-wisps batch is created together ────────
	t.Run("all_wisps_batch_created", func(t *testing.T) {
		te := newTestEnv(t, "aw")
		ctx := t.Context()

		issues := []*types.Issue{
			{ID: "aw-wisp-1", Title: "Wisp 1", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true},
			{ID: "aw-wisp-2", Title: "Wisp 2", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true},
			{ID: "aw-wisp-3", Title: "Wisp 3", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true},
		}
		if err := te.store.CreateIssues(ctx, issues, "tester"); err != nil {
			t.Fatalf("CreateIssues(all wisps): %v", err)
		}
		for _, id := range []string{"aw-wisp-1", "aw-wisp-2", "aw-wisp-3"} {
			te.assertRowExists(t, ctx, "wisps", id)
		}
	})

	// ── failure: a mid-batch prefix-validation error rolls back EVERY wisp ────
	// The batch is all-wisps (fast path) with a bad-prefix second wisp. Because
	// CreateIssues uses SkipPrefixValidation=false, wisp 2 fails ValidateIssueIDPrefix.
	// Buggy per-wisp-commit: wisp 1 is already committed when wisp 2 fails.
	// Fixed single-tx: neither wisp is persisted.
	t.Run("mid_batch_failure_rolls_back_all", func(t *testing.T) {
		te := newTestEnv(t, "awx")
		ctx := t.Context()

		good := &types.Issue{ID: "awx-wisp-1", Title: "Good wisp", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
		// Bad prefix: does not start with "awx-", so ValidateIssueIDPrefix fails.
		bad := &types.Issue{ID: "wrong-wisp-2", Title: "Bad-prefix wisp", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}

		err := te.store.CreateIssues(ctx, []*types.Issue{good, bad}, "tester")
		if err == nil {
			t.Fatal("expected CreateIssues to fail on bad-prefix wisp, got nil")
		}
		if !errors.Is(err, storage.ErrPrefixMismatch) {
			t.Fatalf("expected ErrPrefixMismatch, got: %v", err)
		}

		// The good wisp that preceded the failure must NOT be committed.
		te.assertRowNotExists(t, ctx, "wisps", "awx-wisp-1")
		te.assertRowNotExists(t, ctx, "wisps", "wrong-wisp-2")
	})
}
