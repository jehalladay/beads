//go:build cgo

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
)

// beads-xu7q9: `bd rename-prefix` (renamePrefixInDB) and `--repair`
// (repairPrefixes) previously renamed every issue via a loop of
// self-committing store.UpdateIssueID calls plus a trailing store.SetConfig.
// Each UpdateIssueID committed independently, so a DB-level fault partway
// through the loop left the DB PARTIALLY renamed — some issues carrying the new
// prefix, some the old — a corrupt mixed-prefix state. For --repair (whose whole
// job is to CURE a multi-prefix DB) that is strictly worse than the input.
//
// The fix runs the whole rename loop + config update in ONE
// store.RunInTransaction, so a mid-loop failure rolls the entire rename back and
// the DB keeps its original (single-prefix) state intact.
//
// These teeth build two issues, then drive renamePrefixInDB through a store
// whose tx-level UpdateIssueID faults on the SECOND rename, and assert (a) the
// call errors and (b) NO issue was renamed (full rollback — not just the second).
//
// MUTATION-VERIFY: revert renamePrefixInDB to the per-issue self-committing loop
// (store.UpdateIssueID + store.SetConfig outside a tx) and this test FAILS — the
// first issue's rename commits before the second faults, leaving a mixed-prefix
// DB.

var errInjectedRenameFailure = errors.New("injected rename failure (xu7q9 test)")

// faultRenameStore faults UpdateIssueID on both the transactional path (the
// fix) and the non-transactional store path (the reverted pre-fix shape), after
// the first N successful renames, so the mutation-verify is honest.
type faultRenameStore struct {
	storage.DoltStorage
	failAfter int // allow this many UpdateIssueID calls, then fault
	calls     *int
}

func (f *faultRenameStore) UpdateIssueID(ctx context.Context, oldID, newID string, issue *types.Issue, actor string) error {
	*f.calls++
	if *f.calls > f.failAfter {
		return errInjectedRenameFailure
	}
	return f.DoltStorage.UpdateIssueID(ctx, oldID, newID, issue, actor)
}

func (f *faultRenameStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultRenameTx{Transaction: tx, parent: f})
	})
}

type faultRenameTx struct {
	storage.Transaction
	parent *faultRenameStore
}

func (t *faultRenameTx) UpdateIssueID(ctx context.Context, oldID, newID string, issue *types.Issue, actor string) error {
	*t.parent.calls++
	if *t.parent.calls > t.parent.failAfter {
		return errInjectedRenameFailure
	}
	return t.Transaction.UpdateIssueID(ctx, oldID, newID, issue, actor)
}

func countIssuesWithPrefix(t *testing.T, real storage.DoltStorage, prefix string) int {
	t.Helper()
	all, err := real.SearchIssues(context.Background(), "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	n := 0
	for _, is := range all {
		if utils.ExtractIssuePrefix(is.ID) == prefix {
			n++
		}
	}
	return n
}

func TestRenamePrefixIsAtomic_xu7q9(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "old")
	ctx := context.Background()

	// Build two issues under the "old" prefix.
	for i := 0; i < 2; i++ {
		iss := &types.Issue{
			ID:        "",
			Title:     "rename fixture",
			IssueType: types.TypeTask,
			Priority:  2,
			Status:    types.StatusOpen,
		}
		if err := real.CreateIssue(ctx, iss, "test-actor"); err != nil {
			t.Fatalf("create fixture %d: %v", i, err)
		}
	}

	beforeOld := countIssuesWithPrefix(t, real, "old")
	if beforeOld < 2 {
		t.Fatalf("expected >=2 issues under the old prefix, got %d", beforeOld)
	}
	issues, err := real.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}

	// Fault the SECOND rename: the first would commit under the pre-fix
	// per-issue-commit shape, leaving a mixed-prefix DB.
	calls := 0
	fault := &faultRenameStore{DoltStorage: real, failAfter: 1, calls: &calls}

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
	store = fault
	rootCtx = ctx
	jsonOutput = true
	actor = "test-actor"

	if err := renamePrefixInDB(ctx, "old", "new", issues); err == nil {
		t.Fatalf("expected renamePrefixInDB to error when the 2nd rename faults; got nil")
	}

	// Full rollback: NO issue should carry the new prefix, all still old.
	store = real
	if got := countIssuesWithPrefix(t, real, "new"); got != 0 {
		t.Errorf("REGRESSION (xu7q9): %d issues carry the NEW prefix after a mid-loop failure, want 0 — the rename is non-atomic (a partial rename committed before the fault)", got)
	}
	if got := countIssuesWithPrefix(t, real, "old"); got != beforeOld {
		t.Errorf("REGRESSION (xu7q9): %d issues under the OLD prefix after a failed rename, want %d — a partial rename committed (mixed-prefix DB)", got, beforeOld)
	}
	// The prefix config must not have advanced either.
	if p, _ := real.GetConfig(ctx, "issue_prefix"); p != "old" {
		t.Errorf("REGRESSION (xu7q9): issue_prefix config = %q after a failed rename, want \"old\" — config advanced despite the rename rolling back", p)
	}
}
