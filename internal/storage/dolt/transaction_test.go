package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

func TestRunInTransactionIgnoredWritesStayOnActiveBranch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	branch, err := store.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}

	wispID := "test-wisp-branch-local"
	wisp := &types.Issue{
		ID:        wispID,
		Title:     "branch-local ignored tx wisp",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	if err := store.RunInTransaction(ctx, "test: create branch-local wisp", func(tx storage.Transaction) error {
		return tx.CreateIssue(ctx, wisp, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction create wisp: %v", err)
	}

	assertWispCount(ctx, t, store.db, wispID, 1)

	if err := store.Checkout(ctx, "main"); err != nil {
		t.Fatalf("checkout main: %v", err)
	}
	assertWispCount(ctx, t, store.db, wispID, 0)

	if err := store.Checkout(ctx, branch); err != nil {
		t.Fatalf("checkout %s: %v", branch, err)
	}
	assertWispCount(ctx, t, store.db, wispID, 1)
}

func TestRunInTransactionWispCreatePersistsInitialSideTables(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	createdAt := time.Date(2026, 5, 22, 6, 0, 0, 0, time.UTC)
	wisp := &types.Issue{
		ID:        "test-wisp-tx-side-tables",
		Title:     "transactional wisp with initial side tables",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
		Labels:    []string{"alpha", "beta"},
		Comments: []*types.Comment{{
			Author:    "tester",
			Text:      "seed comment",
			CreatedAt: createdAt,
		}},
	}
	if err := store.RunInTransaction(ctx, "test: create wisp side tables", func(tx storage.Transaction) error {
		return tx.CreateIssue(ctx, wisp, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction create wisp: %v", err)
	}

	assertWispCount(ctx, t, store.db, wisp.ID, 1)
	assertTableCount(ctx, t, store.db, "wisp_labels", wisp.ID, 2)
	assertTableCount(ctx, t, store.db, "wisp_comments", wisp.ID, 1)

	var labelEventCount int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM wisp_events WHERE issue_id = ? AND event_type = ?",
		wisp.ID, types.EventLabelAdded,
	).Scan(&labelEventCount); err != nil {
		t.Fatalf("query wisp label events for %s: %v", wisp.ID, err)
	}
	if labelEventCount != 2 {
		t.Fatalf("wisp label event count for %s = %d, want 2", wisp.ID, labelEventCount)
	}
}

func TestRunInTransactionCreateIssuesMixedWispReadYourWrites(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	regular := &types.Issue{
		ID:        "test-mixed-batch-regular",
		Title:     "regular issue in mixed transaction batch",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	wisp := &types.Issue{
		ID:        "test-mixed-batch-wisp",
		Title:     "wisp issue in mixed transaction batch",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
		Labels:    []string{"seed"},
	}
	if err := store.RunInTransaction(ctx, "test: create mixed transaction batch", func(tx storage.Transaction) error {
		if err := tx.CreateIssues(ctx, []*types.Issue{regular, wisp}, "tester"); err != nil {
			return err
		}
		got, err := tx.GetIssue(ctx, wisp.ID)
		if err != nil {
			return err
		}
		if got.ID != wisp.ID || !got.Ephemeral {
			return fmt.Errorf("GetIssue(%s) = %+v, want active wisp", wisp.ID, got)
		}
		if err := tx.AddLabel(ctx, wisp.ID, "txn", "tester"); err != nil {
			return err
		}
		labels, err := tx.GetLabels(ctx, wisp.ID)
		if err != nil {
			return err
		}
		if len(labels) != 2 || labels[0] != "seed" || labels[1] != "txn" {
			return fmt.Errorf("wisp labels in tx = %v, want [seed txn]", labels)
		}
		return nil
	}); err != nil {
		t.Fatalf("RunInTransaction mixed CreateIssues: %v", err)
	}

	assertIssueCount(ctx, t, store.db, regular.ID, 1)
	assertWispCount(ctx, t, store.db, wisp.ID, 1)
	assertTableCount(ctx, t, store.db, "wisp_labels", wisp.ID, 2)
}

func TestRunInTransactionCreateIssuesAllWispBatchReconcilesChildCounters(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	parent := &types.Issue{
		ID:        "test-tx-wisp-parent",
		Title:     "transactional wisp parent",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	child := &types.Issue{
		ID:        parent.ID + ".3",
		Title:     "transactional wisp child",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	if err := store.RunInTransaction(ctx, "test: create wisp transaction batch", func(tx storage.Transaction) error {
		return tx.CreateIssues(ctx, []*types.Issue{parent, child}, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction all-wisp CreateIssues: %v", err)
	}

	var lastChild int
	if err := store.db.QueryRowContext(ctx,
		"SELECT last_child FROM wisp_child_counters WHERE parent_id = ?",
		parent.ID,
	).Scan(&lastChild); err != nil {
		t.Fatalf("read wisp child counter: %v", err)
	}
	if lastChild != 3 {
		t.Fatalf("wisp last_child = %d, want 3", lastChild)
	}
}

func TestValidateCreateIssuesMixedBucketDependenciesRejectsCrossBucketEdges(t *testing.T) {
	regularA := &types.Issue{ID: "test-regular-a", IssueType: types.TypeTask}
	regularB := &types.Issue{ID: "test-regular-b", IssueType: types.TypeTask}
	wispA := &types.Issue{ID: "test-wisp-a", IssueType: types.TypeTask, Ephemeral: true}
	wispB := &types.Issue{ID: "test-wisp-b", IssueType: types.TypeTask, Ephemeral: true}

	tests := []struct {
		name      string
		regulars  []*types.Issue
		wisps     []*types.Issue
		wantError bool
	}{
		{
			name: "regular to wisp",
			regulars: []*types.Issue{{
				ID:        regularA.ID,
				IssueType: types.TypeTask,
				Dependencies: []*types.Dependency{{
					DependsOnID: wispA.ID,
					Type:        types.DepBlocks,
				}},
			}},
			wisps:     []*types.Issue{wispA},
			wantError: true,
		},
		{
			name:     "wisp to regular",
			regulars: []*types.Issue{regularA},
			wisps: []*types.Issue{{
				ID:        wispA.ID,
				IssueType: types.TypeTask,
				Ephemeral: true,
				Dependencies: []*types.Dependency{{
					DependsOnID: regularA.ID,
					Type:        types.DepBlocks,
				}},
			}},
			wantError: true,
		},
		{
			name: "same bucket dependencies",
			regulars: []*types.Issue{
				regularB,
				{
					ID:        regularA.ID,
					IssueType: types.TypeTask,
					Dependencies: []*types.Dependency{{
						DependsOnID: regularB.ID,
						Type:        types.DepBlocks,
					}},
				},
			},
			wisps: []*types.Issue{
				wispB,
				{
					ID:        wispA.ID,
					IssueType: types.TypeTask,
					Ephemeral: true,
					Dependencies: []*types.Dependency{{
						DependsOnID: wispB.ID,
						Type:        types.DepBlocks,
					}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := append(append([]*types.Issue{}, tt.regulars...), tt.wisps...)
			err := issueops.ValidateCreateIssuesMixedBucketDependencies(issues)
			if tt.wantError {
				if err == nil || !strings.Contains(err.Error(), "cross-bucket dependency") {
					t.Fatalf("error = %v, want cross-bucket dependency", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

func TestRunInTransactionCreateIssuesRejectsRegularToWispBatchDependency(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	regular := &types.Issue{
		ID:        "test-mixed-batch-regular-dep-source",
		Title:     "regular issue with wisp dependency",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "test-mixed-batch-wisp-dep-target",
			Type:        types.DepBlocks,
		}},
	}
	wisp := &types.Issue{
		ID:        "test-mixed-batch-wisp-dep-target",
		Title:     "wisp dependency target",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	err := store.RunInTransaction(ctx, "test: reject regular-to-wisp batch dependency", func(tx storage.Transaction) error {
		return tx.CreateIssues(ctx, []*types.Issue{regular, wisp}, "tester")
	})
	if err == nil || !strings.Contains(err.Error(), "cross-bucket dependency") {
		t.Fatalf("RunInTransaction mixed CreateIssues error = %v, want cross-bucket dependency", err)
	}

	assertIssueCount(ctx, t, store.db, regular.ID, 0)
	assertWispCount(ctx, t, store.db, wisp.ID, 0)
}

func TestRunInTransactionCreateIssuesRejectsWispToRegularBatchDependency(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	regular := &types.Issue{
		ID:        "test-mixed-batch-regular-dep-target",
		Title:     "regular dependency target",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	wisp := &types.Issue{
		ID:        "test-mixed-batch-wisp-dep-source",
		Title:     "wisp issue with regular dependency",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
		Dependencies: []*types.Dependency{{
			DependsOnID: regular.ID,
			Type:        types.DepBlocks,
		}},
	}
	err := store.RunInTransaction(ctx, "test: reject wisp-to-regular batch dependency", func(tx storage.Transaction) error {
		return tx.CreateIssues(ctx, []*types.Issue{regular, wisp}, "tester")
	})
	if err == nil || !strings.Contains(err.Error(), "cross-bucket dependency") {
		t.Fatalf("RunInTransaction mixed CreateIssues error = %v, want cross-bucket dependency", err)
	}

	assertIssueCount(ctx, t, store.db, regular.ID, 0)
	assertWispCount(ctx, t, store.db, wisp.ID, 0)
}

func TestRunInTransactionCreateIssuesSkipsExplicitIDPrefixValidation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	issue := &types.Issue{
		ID:        "foreign-explicit-batch-id",
		Title:     "explicit ID outside configured prefix",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := store.RunInTransaction(ctx, "test: create explicit id batch", func(tx storage.Transaction) error {
		return tx.CreateIssues(ctx, []*types.Issue{issue}, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction explicit-ID CreateIssues: %v", err)
	}

	assertIssueCount(ctx, t, store.db, issue.ID, 1)
}

func assertIssueCount(ctx context.Context, t *testing.T, db *sql.DB, id string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("query issue count for %s: %v", id, err)
	}
	if got != want {
		t.Fatalf("issue count for %s = %d, want %d", id, got, want)
	}
}

func assertWispCount(ctx context.Context, t *testing.T, db *sql.DB, id string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisps WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("query wisp count for %s: %v", id, err)
	}
	if got != want {
		t.Fatalf("wisp count for %s = %d, want %d", id, got, want)
	}
}

func assertTableCount(ctx context.Context, t *testing.T, db *sql.DB, table, id string, want int) {
	t.Helper()
	var got int
	query := "SELECT COUNT(*) FROM " + table + " WHERE issue_id = ?"
	if err := db.QueryRowContext(ctx, query, id).Scan(&got); err != nil {
		t.Fatalf("query %s count for %s: %v", table, id, err)
	}
	if got != want {
		t.Fatalf("%s count for %s = %d, want %d", table, id, got, want)
	}
}

// TestTxGetIssueHydratesLabels proves the storage.Transaction read-your-writes
// GetIssue path hydrates .Labels in the same transaction, mirroring the live
// issueops.GetIssueInTx path (beads-5rn1c). doltTransaction.GetIssue is the
// lone hand-copied GetIssue variant; before the fix it scanned the row and
// returned with Labels=nil (the sibling drift to the kyr9q filter-drop on this
// same object). Covers BOTH tables — a regular issue (labels) and an active
// wisp (wisp_labels) — since the label table is routed by wisp status.
func TestTxGetIssueHydratesLabels(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	regular := &types.Issue{
		ID:        "test-5rn1c-regular",
		Title:     "regular issue with labels for in-tx GetIssue",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Labels:    []string{"beta", "alpha"}, // stored sorted -> [alpha beta]
	}
	wisp := &types.Issue{
		ID:        "test-5rn1c-wisp",
		Title:     "wisp with labels for in-tx GetIssue",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
		Labels:    []string{"gamma"},
	}
	if err := store.RunInTransaction(ctx, "test: 5rn1c create labeled issue+wisp", func(tx storage.Transaction) error {
		return tx.CreateIssues(ctx, []*types.Issue{regular, wisp}, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction create labeled issue+wisp: %v", err)
	}

	// Read them back via the in-tx GetIssue path and assert .Labels is hydrated.
	if err := store.RunInTransaction(ctx, "test: 5rn1c in-tx GetIssue labels", func(tx storage.Transaction) error {
		gotReg, err := tx.GetIssue(ctx, regular.ID)
		if err != nil {
			return err
		}
		if len(gotReg.Labels) != 2 || gotReg.Labels[0] != "alpha" || gotReg.Labels[1] != "beta" {
			return fmt.Errorf("in-tx GetIssue(%s).Labels = %v, want [alpha beta] (labels dropped, beads-5rn1c)", regular.ID, gotReg.Labels)
		}

		gotWisp, err := tx.GetIssue(ctx, wisp.ID)
		if err != nil {
			return err
		}
		if len(gotWisp.Labels) != 1 || gotWisp.Labels[0] != "gamma" {
			return fmt.Errorf("in-tx GetIssue(%s).Labels = %v, want [gamma] (wisp_labels dropped, beads-5rn1c)", wisp.ID, gotWisp.Labels)
		}
		return nil
	}); err != nil {
		t.Fatalf("in-tx GetIssue label hydration: %v", err)
	}
}

// TestTxSearchIssuesMergesNoHistoryWisps gives beads-nyhdd teeth: a non-ephemeral
// in-tx SearchIssues must return NoHistory beads (ephemeral=0, stored in the wisps
// table — GH#3649/#3659) while still excluding TRUE ephemeral wisps on an
// Ephemeral=&false filter, mirroring the live store + embedded tx paths which
// merge the wisps table. Before the fix the in-tx path read only the issues table
// (unless Ephemeral==true), silently dropping every NoHistory bead.
func TestTxSearchIssuesMergesNoHistoryWisps(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	regular := &types.Issue{
		ID:        "test-nyhdd-regular",
		Title:     "regular issue for nyhdd merge",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	noHistory := &types.Issue{
		ID:        "test-nyhdd-nohistory",
		Title:     "no-history bead in wisps for nyhdd merge",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		NoHistory: true, // stored in wisps with ephemeral=0
	}
	ephemeral := &types.Issue{
		ID:        "test-nyhdd-ephemeral",
		Title:     "true ephemeral wisp for nyhdd merge",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	if err := store.RunInTransaction(ctx, "test: nyhdd create regular+nohistory+ephemeral", func(tx storage.Transaction) error {
		return tx.CreateIssues(ctx, []*types.Issue{regular, noHistory, ephemeral}, "tester")
	}); err != nil {
		t.Fatalf("RunInTransaction create nyhdd fixtures: %v", err)
	}

	contains := func(issues []*types.Issue, id string) bool {
		for _, is := range issues {
			if is.ID == id {
				return true
			}
		}
		return false
	}

	if err := store.RunInTransaction(ctx, "test: nyhdd in-tx SearchIssues merges wisps", func(tx storage.Transaction) error {
		// Ephemeral==nil: search everything. The NoHistory bead lives in wisps and
		// MUST be returned; the true ephemeral is also returned (search-everything).
		all, err := tx.SearchIssues(ctx, "", types.IssueFilter{})
		if err != nil {
			return err
		}
		if !contains(all, regular.ID) {
			return fmt.Errorf("Ephemeral=nil search missing regular %s (got %d issues, beads-nyhdd)", regular.ID, len(all))
		}
		if !contains(all, noHistory.ID) {
			return fmt.Errorf("Ephemeral=nil search missing NoHistory bead %s — wisps table not merged (beads-nyhdd); got %d issues", noHistory.ID, len(all))
		}

		// Ephemeral=&false: non-ephemeral only. NoHistory (ephemeral=0) must
		// survive; the true ephemeral must be excluded.
		no := false
		nonEph, err := tx.SearchIssues(ctx, "", types.IssueFilter{Ephemeral: &no})
		if err != nil {
			return err
		}
		if !contains(nonEph, noHistory.ID) {
			return fmt.Errorf("Ephemeral=&false search missing NoHistory bead %s — wisps not merged (beads-nyhdd)", noHistory.ID)
		}
		if !contains(nonEph, regular.ID) {
			return fmt.Errorf("Ephemeral=&false search missing regular %s (beads-nyhdd)", regular.ID)
		}
		if contains(nonEph, ephemeral.ID) {
			return fmt.Errorf("Ephemeral=&false search leaked true ephemeral %s (beads-nyhdd)", ephemeral.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("in-tx SearchIssues NoHistory merge: %v", err)
	}
}
