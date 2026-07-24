package issueops

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestManageStaleDefer_0oixp pins the beads-0oixp fix: the shared-seam leg of the
// beads-l2lb7 stale-future-defer_until clear. l2lb7 originally lived ONLY in the
// CLI regularUpdates block (cmd/bd/update.go), so callers that write straight
// through UpdateIssueInTx below the CLI guard layer — notably
// `bd batch update <id> status=open` (batch.go → tx.UpdateIssue) — bypassed it
// and left a stale future defer_until, producing the self-contradictory
// status=open-but-invisible-to-`bd ready` row (ready predicate:
// defer_until IS NULL OR defer_until <= UTC_TIMESTAMP()).
//
// ManageStaleDefer is invoked in updateIssueInTx alongside ManageClosedAt/
// ManageStartedAt so the shared write path (batch/graph-apply/programmatic) and
// the domain/proxied twin (beads-9vy58) all clear a stale future defer_until on
// a flip to a ready-visible status. Mutation check: drop the ManageStaleDefer
// call in updateIssueInTx and the batch status-flip regains the stale defer.
func TestManageStaleDefer_0oixp(t *testing.T) {
	t.Parallel()

	future := func() *time.Time { d := time.Now().Add(72 * time.Hour); return &d }
	past := func() *time.Time { d := time.Now().Add(-72 * time.Hour); return &d }

	t.Run("flip to open clears a stale future defer_until", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusDeferred, DeferUntil: future()}
		updates := map[string]interface{}{"status": string(types.StatusOpen)}
		clauses, args := ManageStaleDefer(old, updates, nil, nil)
		if len(clauses) != 1 || clauses[0] != "defer_until = ?" {
			t.Fatalf("expected [defer_until = ?], got %v", clauses)
		}
		if args[0] != nil {
			t.Fatalf("expected NULL (nil) defer_until arg, got %v", args[0])
		}
	})

	t.Run("flip to in_progress clears a stale future defer_until", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusDeferred, DeferUntil: future()}
		updates := map[string]interface{}{"status": types.StatusInProgress} // types.Status value form
		clauses, args := ManageStaleDefer(old, updates, nil, nil)
		if len(clauses) != 1 || clauses[0] != "defer_until = ?" || args[0] != nil {
			t.Fatalf("expected NULL defer_until clause for in_progress, got clauses=%v args=%v", clauses, args)
		}
	})

	t.Run("caller-set defer_until is not clobbered", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusDeferred, DeferUntil: future()}
		updates := map[string]interface{}{"status": string(types.StatusOpen), "defer_until": *future()}
		clauses, _ := ManageStaleDefer(old, updates, nil, nil)
		if len(clauses) != 0 {
			t.Fatalf("expected no auto clause when defer_until explicit, got %v", clauses)
		}
	})

	t.Run("past defer_until is left untouched", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusDeferred, DeferUntil: past()}
		updates := map[string]interface{}{"status": string(types.StatusOpen)}
		clauses, args := ManageStaleDefer(old, updates, nil, nil)
		if len(clauses) != 0 || len(args) != 0 {
			t.Fatalf("expected no clause for a past (already ready-visible) defer_until, got clauses=%v", clauses)
		}
	})

	t.Run("nil defer_until is a no-op", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		updates := map[string]interface{}{"status": string(types.StatusOpen)}
		clauses, args := ManageStaleDefer(old, updates, nil, nil)
		if len(clauses) != 0 || len(args) != 0 {
			t.Fatalf("expected no clause when defer_until is nil, got clauses=%v", clauses)
		}
	})

	t.Run("flip to a non-ready-visible status does not clear defer_until", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusDeferred, DeferUntil: future()}
		updates := map[string]interface{}{"status": string(types.StatusBlocked)}
		clauses, _ := ManageStaleDefer(old, updates, nil, nil)
		if len(clauses) != 0 {
			t.Fatalf("expected no clause for blocked transition (mirrors l2lb7 scope), got %v", clauses)
		}
	})

	t.Run("no status change leaves clauses untouched", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusDeferred, DeferUntil: future()}
		clauses, args := ManageStaleDefer(old, map[string]interface{}{"title": "x"}, nil, nil)
		if len(clauses) != 0 || len(args) != 0 {
			t.Fatalf("expected no clauses on a non-status update, got clauses=%v args=%v", clauses, args)
		}
	})

	t.Run("nil oldIssue is a safe no-op", func(t *testing.T) {
		t.Parallel()
		clauses, args := ManageStaleDefer(nil, map[string]interface{}{"status": string(types.StatusOpen)}, nil, nil)
		if len(clauses) != 0 || len(args) != 0 {
			t.Fatalf("expected no clauses for nil oldIssue, got clauses=%v args=%v", clauses, args)
		}
	})

	t.Run("non-string/non-Status status value is ignored", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusDeferred, DeferUntil: future()}
		clauses, _ := ManageStaleDefer(old, map[string]interface{}{"status": 42}, nil, nil)
		if len(clauses) != 0 {
			t.Fatalf("expected no clause for a non-string status, got %v", clauses)
		}
	})
}
