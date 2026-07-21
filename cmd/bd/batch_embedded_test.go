//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// runBatchScriptInTx is a tiny helper that mirrors what batchCmd.RunE does,
// minus the cobra/flag plumbing, so tests can drive batch execution against
// a *dolt.DoltStore without spawning a 'bd' subprocess.
func runBatchScriptInTx(t *testing.T, ctx context.Context, st storage.DoltStorage, script string) error {
	t.Helper()
	ops, err := parseBatchScript(strings.NewReader(script))
	if err != nil {
		return err
	}
	return st.RunInTransaction(ctx, "test: bd batch", func(tx storage.Transaction) error {
		for _, op := range ops {
			if _, err := runBatchOp(ctx, tx, op, ""); err != nil {
				return err
			}
		}
		return nil
	})
}

// runBatchGuarded mirrors what batchCmd.RunE does end-to-end: it runs the
// close-time integrity guard pre-pass (guardBatchCloses) BEFORE opening the
// write transaction, exactly as the real command does. Tests use this (instead
// of runBatchScriptInTx, which drives only runBatchOp) to exercise the guard.
func runBatchGuarded(t *testing.T, ctx context.Context, st storage.DoltStorage, script string, force bool) error {
	t.Helper()
	ops, err := parseBatchScript(strings.NewReader(script))
	if err != nil {
		return err
	}
	if gerr := guardBatchCloses(ctx, st, ops, force); gerr != nil {
		return gerr
	}
	return st.RunInTransaction(ctx, "test: bd batch guarded", func(tx storage.Transaction) error {
		for _, op := range ops {
			if _, err := runBatchOp(ctx, tx, op, ""); err != nil {
				return err
			}
		}
		return nil
	})
}

// seedBatchTestIssues creates three open issues for batch tests to operate on.
func seedBatchTestIssues(t *testing.T, ctx context.Context, st storage.DoltStorage, ids ...string) {
	t.Helper()
	for _, id := range ids {
		issue := &types.Issue{
			ID:        id,
			Title:     "seed " + id,
			Status:    types.StatusOpen,
			Priority:  2,
			IssueType: types.TypeTask,
		}
		if err := st.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("seed CreateIssue %s: %v", id, err)
		}
	}
}

// TestBatch_AppliesAllInOneTransaction verifies that a batch with several
// supported operations commits atomically and all writes are visible
// afterwards.
func TestBatch_AppliesAllInOneTransaction(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tb")
	ctx := context.Background()

	seedBatchTestIssues(t, ctx, st, "tb-1", "tb-2", "tb-3")

	script := `# batch test: close one, update one, link two
close tb-1 done in batch
update tb-2 status=in_progress priority=1
dep add tb-3 tb-2
`
	if err := runBatchScriptInTx(t, ctx, st, script); err != nil {
		t.Fatalf("batch run: %v", err)
	}

	// Verify the close
	got1, err := st.GetIssue(ctx, "tb-1")
	if err != nil {
		t.Fatalf("GetIssue tb-1: %v", err)
	}
	if got1.Status != types.StatusClosed {
		t.Errorf("tb-1 status = %q, want closed", got1.Status)
	}

	// Verify the update
	got2, err := st.GetIssue(ctx, "tb-2")
	if err != nil {
		t.Fatalf("GetIssue tb-2: %v", err)
	}
	if string(got2.Status) != "in_progress" {
		t.Errorf("tb-2 status = %q, want in_progress", got2.Status)
	}
	if got2.Priority != 1 {
		t.Errorf("tb-2 priority = %d, want 1", got2.Priority)
	}

	// Verify the dependency
	deps, err := st.GetDependencies(ctx, "tb-3")
	if err != nil {
		t.Fatalf("GetDependencies tb-3: %v", err)
	}
	found := false
	for _, d := range deps {
		if d.ID == "tb-2" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tb-3 to depend on tb-2, got %+v", deps)
	}
}

// TestBatch_RollbackOnError verifies that if any op in the batch fails the
// entire transaction is rolled back and earlier writes are not visible.
//
// The trigger here is `dep add` referencing nonexistent issue IDs, which
// fails the foreign-key constraint on the dependencies table.
func TestBatch_RollbackOnError(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbr")
	ctx := context.Background()

	seedBatchTestIssues(t, ctx, st, "tbr-1", "tbr-2")

	// Op 1 succeeds (close tbr-1), op 2 succeeds (update tbr-2), op 3
	// references nonexistent IDs and must fail (FK violation). The whole tx
	// should roll back; tbr-1 must remain open and tbr-2 must remain P2.
	script := `close tbr-1 should-roll-back
update tbr-2 priority=0
dep add tbr-DOES-NOT-EXIST tbr-ALSO-MISSING
`
	err := runBatchScriptInTx(t, ctx, st, script)
	if err == nil {
		t.Fatal("expected batch to fail because of foreign key violation")
	}

	// tbr-1 should still be open
	got1, gerr := st.GetIssue(ctx, "tbr-1")
	if gerr != nil {
		t.Fatalf("GetIssue tbr-1: %v", gerr)
	}
	if got1.Status == types.StatusClosed {
		t.Errorf("tbr-1 was closed despite rollback (status=%q)", got1.Status)
	}

	// tbr-2 should still be P2
	got2, gerr := st.GetIssue(ctx, "tbr-2")
	if gerr != nil {
		t.Fatalf("GetIssue tbr-2: %v", gerr)
	}
	if got2.Priority != 2 {
		t.Errorf("tbr-2 priority = %d, want 2 (rollback)", got2.Priority)
	}
}

// TestBatch_EmptyScriptIsNoOp verifies that an empty input is treated as a
// successful no-op (matches `bd list ... | bd batch` with an empty pipeline).
func TestBatch_EmptyScriptIsNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbe")
	ctx := context.Background()

	if err := runBatchScriptInTx(t, ctx, st, ""); err != nil {
		t.Errorf("empty script: %v", err)
	}
	if err := runBatchScriptInTx(t, ctx, st, "# only a comment\n\n"); err != nil {
		t.Errorf("comment-only script: %v", err)
	}
}

// TestBatch_UnsupportedCommandFailsBeforeWrites verifies that an unknown
// command anywhere in the input causes a parse-time failure with a clear
// message and no operations are executed (the test confirms the error
// surfaces before any writes hit the database).
func TestBatch_UnsupportedCommandFailsBeforeWrites(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbu")
	ctx := context.Background()

	seedBatchTestIssues(t, ctx, st, "tbu-1")

	script := `close tbu-1 will-not-happen
show tbu-1
`
	err := runBatchScriptInTx(t, ctx, st, script)
	if err == nil {
		t.Fatal("expected error for unsupported command")
	}
	if !strings.Contains(err.Error(), "unsupported batch command") {
		t.Errorf("error should mention unsupported command, got: %v", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should reference the offending line, got: %v", err)
	}

	// Confirm tbu-1 is still open (parse failed before any tx ran).
	got, gerr := st.GetIssue(ctx, "tbu-1")
	if gerr != nil {
		t.Fatalf("GetIssue tbu-1: %v", gerr)
	}
	if got.Status == types.StatusClosed {
		t.Errorf("tbu-1 was closed even though parse should have failed first")
	}
}

// TestBatch_DepRemoveInBatch verifies the dep.remove path inside a batch.
func TestBatch_DepRemoveInBatch(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbd")
	ctx := context.Background()

	seedBatchTestIssues(t, ctx, st, "tbd-1", "tbd-2")
	if err := st.AddDependency(ctx, &types.Dependency{
		IssueID: "tbd-1", DependsOnID: "tbd-2", Type: types.DepBlocks,
	}, "test"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	if err := runBatchScriptInTx(t, ctx, st, "dep remove tbd-1 tbd-2\n"); err != nil {
		t.Fatalf("batch dep remove: %v", err)
	}

	deps, err := st.GetDependencies(ctx, "tbd-1")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	for _, d := range deps {
		if d.ID == "tbd-2" {
			t.Errorf("dependency tbd-1 -> tbd-2 still present after dep remove")
		}
	}
}

// beads-cqk1: `bd batch` dep-add must reject an unknown dependency type the same
// way `bd dep add` does (beads-qfka) — batch's help says "dependency types: see
// bd dep add", but it previously gated IsValid()-only, letting a typo'd blocking
// type ('blockd') store as a non-gating custom edge and roll the batch on rc=0.
func TestBatch_DepAddUnknownTypeRejected(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbu")
	ctx := context.Background()
	seedBatchTestIssues(t, ctx, st, "tbu-1", "tbu-2")

	err := runBatchScriptInTx(t, ctx, st, "dep add tbu-1 tbu-2 blockd\n")
	if err == nil {
		t.Fatal("expected batch dep add with unknown type 'blockd' to fail")
	}
	if !strings.Contains(err.Error(), "unknown dependency type") {
		t.Fatalf("expected 'unknown dependency type' error, got: %v", err)
	}
	// The whole batch must roll back — no edge persisted.
	deps, derr := st.GetDependencies(ctx, "tbu-1")
	if derr != nil {
		t.Fatalf("GetDependencies: %v", derr)
	}
	for _, d := range deps {
		if d.ID == "tbu-2" {
			t.Errorf("rejected unknown-type edge was persisted: %+v", d)
		}
	}
}

func TestBatch_DepAddWellKnownTypeAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbw")
	ctx := context.Background()
	seedBatchTestIssues(t, ctx, st, "tbw-1", "tbw-2")

	if err := runBatchScriptInTx(t, ctx, st, "dep add tbw-1 tbw-2 related\n"); err != nil {
		t.Fatalf("well-known type 'related' should be accepted: %v", err)
	}
}

// TestBatch_CloseGuardParity verifies that `bd batch` enforces the same
// close-time integrity guards as `bd close` / `bd update --status closed`
// (beads-1d08 — the batch sibling of beads-zgku): a blocked issue or an epic
// with open children cannot be closed via batch without --force, and the whole
// batch rolls back on a violation. It also verifies the batch-aware relaxation:
// closing an epic and all its open children in ONE batch succeeds.
func TestBatch_CloseGuardParity(t *testing.T) {
	newStore := func(t *testing.T, prefix string) (storage.DoltStorage, context.Context) {
		tmpDir := t.TempDir()
		st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), prefix)
		return st, context.Background()
	}

	// seedBlocked creates task A blocked by open task B.
	seedBlocked := func(t *testing.T, ctx context.Context, st storage.DoltStorage, prefix string) (a, b string) {
		a, b = prefix+"-A", prefix+"-B"
		seedBatchTestIssues(t, ctx, st, a, b)
		if err := st.AddDependency(ctx, &types.Dependency{
			IssueID: a, DependsOnID: b, Type: types.DepBlocks,
		}, "test"); err != nil {
			t.Fatalf("AddDependency %s blocks: %v", a, err)
		}
		return a, b
	}

	// seedEpicChild creates an epic with one open parent-child child.
	seedEpicChild := func(t *testing.T, ctx context.Context, st storage.DoltStorage, prefix string) (epic, child string) {
		epic, child = prefix+"-E", prefix+"-C"
		if err := st.CreateIssue(ctx, &types.Issue{
			ID: epic, Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic,
		}, "test"); err != nil {
			t.Fatalf("create epic: %v", err)
		}
		if err := st.CreateIssue(ctx, &types.Issue{
			ID: child, Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
		}, "test"); err != nil {
			t.Fatalf("create child: %v", err)
		}
		// parent-child edge: child depends-on epic.
		if err := st.AddDependency(ctx, &types.Dependency{
			IssueID: child, DependsOnID: epic, Type: types.DepParentChild,
		}, "test"); err != nil {
			t.Fatalf("AddDependency parent-child: %v", err)
		}
		return epic, child
	}

	assertOpen := func(t *testing.T, ctx context.Context, st storage.DoltStorage, id string) {
		t.Helper()
		got, err := st.GetIssue(ctx, id)
		if err != nil {
			t.Fatalf("GetIssue %s: %v", id, err)
		}
		if got.Status == types.StatusClosed {
			t.Errorf("%s was closed but should still be open (guard should have refused + rolled back)", id)
		}
	}
	assertClosed := func(t *testing.T, ctx context.Context, st storage.DoltStorage, id string) {
		t.Helper()
		got, err := st.GetIssue(ctx, id)
		if err != nil {
			t.Fatalf("GetIssue %s: %v", id, err)
		}
		if got.Status != types.StatusClosed {
			t.Errorf("%s status = %q, want closed", id, got.Status)
		}
	}

	t.Run("batch_close_blocked_refuses_without_force", func(t *testing.T) {
		st, ctx := newStore(t, "gb1")
		a, _ := seedBlocked(t, ctx, st, "gb1")
		err := runBatchGuarded(t, ctx, st, "close "+a+" attempt\n", false)
		if err == nil {
			t.Fatal("expected batch close of a blocked issue to be refused")
		}
		if !strings.Contains(err.Error(), "blocked by open issues") {
			t.Errorf("error should mention blocked-by-open, got: %v", err)
		}
		assertOpen(t, ctx, st, a)
	})

	t.Run("batch_update_to_closed_blocked_refuses_without_force", func(t *testing.T) {
		st, ctx := newStore(t, "gb2")
		a, _ := seedBlocked(t, ctx, st, "gb2")
		err := runBatchGuarded(t, ctx, st, "update "+a+" status=closed\n", false)
		if err == nil {
			t.Fatal("expected batch update-to-closed of a blocked issue to be refused")
		}
		if !strings.Contains(err.Error(), "blocked by open issues") {
			t.Errorf("error should mention blocked-by-open, got: %v", err)
		}
		assertOpen(t, ctx, st, a)
	})

	t.Run("batch_close_blocked_with_force_succeeds", func(t *testing.T) {
		st, ctx := newStore(t, "gb3")
		a, _ := seedBlocked(t, ctx, st, "gb3")
		if err := runBatchGuarded(t, ctx, st, "close "+a+" forced\n", true); err != nil {
			t.Fatalf("--force batch close should succeed: %v", err)
		}
		assertClosed(t, ctx, st, a)
	})

	t.Run("batch_close_blocked_when_blocker_closed_same_batch_succeeds", func(t *testing.T) {
		// Batch-aware: closing the blocker B and the blocked A in one batch is OK.
		st, ctx := newStore(t, "gb4")
		a, b := seedBlocked(t, ctx, st, "gb4")
		script := "close " + b + " blocker first\nclose " + a + " now unblocked\n"
		if err := runBatchGuarded(t, ctx, st, script, false); err != nil {
			t.Fatalf("closing blocker + blocked in one batch should succeed: %v", err)
		}
		assertClosed(t, ctx, st, a)
		assertClosed(t, ctx, st, b)
	})

	t.Run("batch_close_epic_open_children_refuses_without_force", func(t *testing.T) {
		st, ctx := newStore(t, "gb5")
		epic, child := seedEpicChild(t, ctx, st, "gb5")
		err := runBatchGuarded(t, ctx, st, "close "+epic+" attempt\n", false)
		if err == nil {
			t.Fatal("expected batch close of an epic with open children to be refused")
		}
		if !strings.Contains(err.Error(), "open child issue") {
			t.Errorf("error should mention open child issue(s), got: %v", err)
		}
		assertOpen(t, ctx, st, epic)
		// child must not be orphaned/closed by the refused batch.
		assertOpen(t, ctx, st, child)
	})

	t.Run("batch_close_epic_with_force_succeeds", func(t *testing.T) {
		st, ctx := newStore(t, "gb6")
		epic, _ := seedEpicChild(t, ctx, st, "gb6")
		if err := runBatchGuarded(t, ctx, st, "close "+epic+" forced\n", true); err != nil {
			t.Fatalf("--force batch close of epic should succeed: %v", err)
		}
		assertClosed(t, ctx, st, epic)
	})

	t.Run("batch_close_epic_and_children_same_batch_succeeds", func(t *testing.T) {
		// Batch-aware: closing all open children AND the epic in one batch is OK,
		// regardless of op order (child listed AFTER the epic here).
		st, ctx := newStore(t, "gb7")
		epic, child := seedEpicChild(t, ctx, st, "gb7")
		script := "close " + epic + " parent\nclose " + child + " child\n"
		if err := runBatchGuarded(t, ctx, st, script, false); err != nil {
			t.Fatalf("closing epic + its only open child in one batch should succeed: %v", err)
		}
		assertClosed(t, ctx, st, epic)
		assertClosed(t, ctx, st, child)
	})

	t.Run("batch_close_unblocked_succeeds", func(t *testing.T) {
		st, ctx := newStore(t, "gb8")
		seedBatchTestIssues(t, ctx, st, "gb8-1")
		if err := runBatchGuarded(t, ctx, st, "close gb8-1 clean\n", false); err != nil {
			t.Fatalf("closing an unblocked non-epic should succeed: %v", err)
		}
		assertClosed(t, ctx, st, "gb8-1")
	})

	t.Run("batch_non_close_update_on_blocked_unaffected", func(t *testing.T) {
		// A non-close status change on a blocked issue must NOT be guarded.
		st, ctx := newStore(t, "gb9")
		a, _ := seedBlocked(t, ctx, st, "gb9")
		if err := runBatchGuarded(t, ctx, st, "update "+a+" status=in_progress\n", false); err != nil {
			t.Fatalf("non-close status update on a blocked issue should not be guarded: %v", err)
		}
		got, err := st.GetIssue(ctx, a)
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		if string(got.Status) != "in_progress" {
			t.Errorf("%s status = %q, want in_progress", a, got.Status)
		}
	})
}

// TestBatch_RollbackTriggerStillFailsAtStorageLayer is a guard against the
// rollback test silently passing if a future change makes
// tx.AddDependency tolerate missing issue IDs. If this test fails (i.e.
// AddDependency stops returning an error for unknown IDs), the rollback test
// must be rewritten to use a different failure trigger.
func TestBatch_RollbackTriggerStillFailsAtStorageLayer(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbg")
	ctx := context.Background()

	err := st.RunInTransaction(ctx, "test: trigger guard", func(tx storage.Transaction) error {
		return tx.AddDependency(ctx, &types.Dependency{
			IssueID:     "tbg-MISSING-A",
			DependsOnID: "tbg-MISSING-B",
			Type:        types.DepBlocks,
		}, "test")
	})
	if err == nil {
		t.Fatal("expected AddDependency on missing IDs to fail; if not, rewrite TestBatch_RollbackOnError")
	}
}
