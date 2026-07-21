//go:build cgo

package dolt

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// createTypedWisp creates an active (ephemeral) wisp with an explicit issue
// type so cross-type blocking (GH#1495) can be exercised on the wisp-source
// path. createWisp hardcodes TypeTask, so this variant is needed for the
// epic-vs-task cases.
func createTypedWisp(t *testing.T, ctx context.Context, store *DoltStore, id string, it types.IssueType) {
	t.Helper()
	issue := &types.Issue{
		ID:        id,
		Title:     "wisp " + id,
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: it,
		Ephemeral: true,
	}
	if err := store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("create typed wisp %s (%s): %v", id, it, err)
	}
}

// TestWispSourceCrossTypeBlockRejected verifies beads-slmql: a wisp-SOURCE
// blocks edge whose source/target issue_type mismatch (task blocks epic, or
// epic blocks task) must be REJECTED by DoltStore.addWispDependency, matching
// the shared issue-source seam (issueops.AddDependencyInTx, GH#1495) and the
// embedded backend (which always routes through that seam).
//
// Before the fix the bespoke wisp-source path never read/compared issue_type,
// so the mismatch was silently ALLOWED on hub-connected sql-server crew — a
// backend-asymmetric enforcement hole (3rd un-mirrored guard on this method
// after beads-i9bui cycle-family and beads-k5oqp audit-event).
func TestWispSourceCrossTypeBlockRejected_slmql(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		taskW  = "slmql-task-w"
		taskW2 = "slmql-task-w2"
		epicW  = "slmql-epic-w"
		epicW2 = "slmql-epic-w2"
	)
	createTypedWisp(t, ctx, store, taskW, types.TypeTask)
	createTypedWisp(t, ctx, store, taskW2, types.TypeTask)
	createTypedWisp(t, ctx, store, epicW, types.TypeEpic)
	createTypedWisp(t, ctx, store, epicW2, types.TypeEpic)

	// task blocks epic -> rejected
	err := store.AddDependency(ctx,
		&types.Dependency{IssueID: taskW, DependsOnID: epicW, Type: types.DepBlocks}, "adder")
	if err == nil {
		t.Errorf("wisp task-blocks-epic: expected rejection (GH#1495, beads-slmql), got nil")
	} else if !strings.Contains(err.Error(), "tasks can only block other tasks") {
		t.Errorf("wisp task-blocks-epic: err = %q, want 'tasks can only block other tasks'", err)
	}

	// epic blocks task -> rejected
	err = store.AddDependency(ctx,
		&types.Dependency{IssueID: epicW, DependsOnID: taskW, Type: types.DepBlocks}, "adder")
	if err == nil {
		t.Errorf("wisp epic-blocks-task: expected rejection (GH#1495, beads-slmql), got nil")
	} else if !strings.Contains(err.Error(), "epics can only block other epics") {
		t.Errorf("wisp epic-blocks-task: err = %q, want 'epics can only block other epics'", err)
	}

	// CONTROL: same-type edges are still allowed.
	if err := store.AddDependency(ctx,
		&types.Dependency{IssueID: taskW, DependsOnID: taskW2, Type: types.DepBlocks}, "adder"); err != nil {
		t.Errorf("wisp task-blocks-task (control): expected allowed, got %v", err)
	}
	if err := store.AddDependency(ctx,
		&types.Dependency{IssueID: epicW, DependsOnID: epicW2, Type: types.DepBlocks}, "adder"); err != nil {
		t.Errorf("wisp epic-blocks-epic (control): expected allowed, got %v", err)
	}
}
