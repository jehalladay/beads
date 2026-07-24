//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// TestBatch_UpdateStatusOpenClearsStaleDefer_0oixp is the WIRING test for
// beads-0oixp: `bd batch update <id> status=open` must clear a stale FUTURE
// defer_until, the same as the single-verb `bd update --status open` path
// (beads-l2lb7) and the domain/proxied twin (beads-9vy58).
//
// batch writes straight to tx.UpdateIssue → issueops.UpdateIssueWithoutEventInTx
// → updateIssueInTx, BELOW the CLI regularUpdates guard layer that held the
// original l2lb7 clear. The fix pushes the clear DOWN into the shared seam via
// ManageStaleDefer (alongside ManageClosedAt/ManageStartedAt), so this path is
// covered too. Without the `ManageStaleDefer` call wired into updateIssueInTx,
// the flip below regains the stale future defer_until and the row is
// status=open-but-invisible-to-`bd ready` (ready predicate:
// defer_until IS NULL OR defer_until <= UTC_TIMESTAMP()).
//
// Mutation check: delete the ManageStaleDefer line in updateIssueInTx → the
// "open"/"in_progress" subtests go RED (defer_until stays set).
func TestBatch_UpdateStatusOpenClearsStaleDefer_0oixp(t *testing.T) {
	// seedDeferred creates one deferred issue with defer_until set to `when`.
	seedDeferred := func(t *testing.T, ctx context.Context, st storage.DoltStorage, id string, when *time.Time) {
		t.Helper()
		issue := &types.Issue{
			ID:         id,
			Title:      "deferred seed " + id,
			Status:     types.StatusDeferred,
			Priority:   2,
			IssueType:  types.TypeTask,
			DeferUntil: when,
		}
		if err := st.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("seed CreateIssue %s: %v", id, err)
		}
		// Verify the seed actually persisted a future defer_until, so a GREEN
		// assertion below can only mean the flip CLEARED it (not that it was
		// never stored).
		got, err := st.GetIssue(ctx, id)
		if err != nil {
			t.Fatalf("seed GetIssue %s: %v", id, err)
		}
		if got.DeferUntil == nil {
			t.Fatalf("seed %s: expected a persisted defer_until, got nil", id)
		}
	}

	future := time.Now().Add(72 * time.Hour)
	past := time.Now().Add(-72 * time.Hour)

	t.Run("status=open clears a stale future defer_until", func(t *testing.T) {
		tmpDir := t.TempDir()
		st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "bd0")
		ctx := context.Background()
		seedDeferred(t, ctx, st, "bd0-1", &future)

		if err := runBatchScriptInTx(t, ctx, st, "update bd0-1 status=open\n"); err != nil {
			t.Fatalf("batch run: %v", err)
		}

		got, err := st.GetIssue(ctx, "bd0-1")
		if err != nil {
			t.Fatalf("GetIssue bd0-1: %v", err)
		}
		if string(got.Status) != "open" {
			t.Errorf("bd0-1 status = %q, want open", got.Status)
		}
		if got.DeferUntil != nil {
			t.Errorf("bd0-1 defer_until = %v, want nil (stale future defer cleared on flip to open, beads-0oixp)", got.DeferUntil)
		}
	})

	t.Run("status=in_progress clears a stale future defer_until", func(t *testing.T) {
		tmpDir := t.TempDir()
		st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "bd1")
		ctx := context.Background()
		seedDeferred(t, ctx, st, "bd1-1", &future)

		if err := runBatchScriptInTx(t, ctx, st, "update bd1-1 status=in_progress\n"); err != nil {
			t.Fatalf("batch run: %v", err)
		}

		got, err := st.GetIssue(ctx, "bd1-1")
		if err != nil {
			t.Fatalf("GetIssue bd1-1: %v", err)
		}
		if string(got.Status) != "in_progress" {
			t.Errorf("bd1-1 status = %q, want in_progress", got.Status)
		}
		if got.DeferUntil != nil {
			t.Errorf("bd1-1 defer_until = %v, want nil (stale future defer cleared on flip to in_progress, beads-0oixp)", got.DeferUntil)
		}
	})

	t.Run("status=blocked leaves defer_until intact (mirrors l2lb7 scope)", func(t *testing.T) {
		tmpDir := t.TempDir()
		st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "bd2")
		ctx := context.Background()
		seedDeferred(t, ctx, st, "bd2-1", &future)

		if err := runBatchScriptInTx(t, ctx, st, "update bd2-1 status=blocked\n"); err != nil {
			t.Fatalf("batch run: %v", err)
		}

		got, err := st.GetIssue(ctx, "bd2-1")
		if err != nil {
			t.Fatalf("GetIssue bd2-1: %v", err)
		}
		if got.DeferUntil == nil {
			t.Errorf("bd2-1 defer_until was cleared on a flip to blocked; only ready-visible statuses clear (beads-0oixp)")
		}
	})

	t.Run("a past defer_until is left untouched on flip to open", func(t *testing.T) {
		tmpDir := t.TempDir()
		st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "bd3")
		ctx := context.Background()
		seedDeferred(t, ctx, st, "bd3-1", &past)

		if err := runBatchScriptInTx(t, ctx, st, "update bd3-1 status=open\n"); err != nil {
			t.Fatalf("batch run: %v", err)
		}

		got, err := st.GetIssue(ctx, "bd3-1")
		if err != nil {
			t.Fatalf("GetIssue bd3-1: %v", err)
		}
		// A past defer_until is already ready-visible; ManageStaleDefer only
		// fires on a FUTURE defer, so the stored value is left as-is (no clause).
		if got.DeferUntil == nil {
			t.Errorf("bd3-1 past defer_until was cleared; only a FUTURE defer is stale-cleared (beads-0oixp)")
		}
	})
}
