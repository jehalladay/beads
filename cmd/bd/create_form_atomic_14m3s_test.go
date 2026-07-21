//go:build cgo

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-14m3s: the create-FORM sibling of beads-a8d14. CreateIssueFromFormValues
// previously self-committed the issue via store.CreateIssue and THEN added each
// --parent / --deps edge best-effort (Warning to stderr + continue on failure),
// so a form-driven create whose issue succeeded but whose edge write failed left
// a durable issue MISSING its declared edges at a nil error / rc=0 — while the
// atomic proxied create twin buffers the same writes on one UOW. The fix wraps
// CreateIssue + all AddDependency calls in one transactHonoringAutoCommit (the
// exact create.go a8d14 shape) so an edge failure rolls the issue back too and
// the function returns an error.
//
// These teeth call the REAL CreateIssueFromFormValues against a fault-injecting
// store whose Nth AddDependency fails, and assert (a) it returns an error and
// (b) NO orphaned issue survives the failed edge write.
//
// MUTATION-VERIFY: revert create_form.go to the pre-fix shape (self-committing
// store.CreateIssue + best-effort store.AddDependency with a Warning), and these
// tests FAIL — the issue is committed, the edge is dropped, the function returns
// (issue, nil), and the orphan is found by SearchIssues.

// faultCreateFormDepStore embeds the real DoltStorage and, for the transactional
// path, wraps the tx so the Nth AddDependency (failOnAddDep, 1-based across the
// whole run) returns an error. It also faults the non-transactional
// store.AddDependency so a reverted (pre-fix) create_form.go that calls
// store.AddDependency directly still hits the injected failure — this is what
// makes the mutation-verify honest.
type faultCreateFormDepStore struct {
	storage.DoltStorage
	addDepCalls  int
	failOnAddDep int
}

var errInjectedCreateFormDepFailure = errors.New("injected dependency failure (14m3s test)")

func (f *faultCreateFormDepStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultCreateFormDepTx{Transaction: tx, parent: f})
	})
}

func (f *faultCreateFormDepStore) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	f.addDepCalls++
	if f.addDepCalls == f.failOnAddDep {
		return errInjectedCreateFormDepFailure
	}
	return f.DoltStorage.AddDependency(ctx, dep, actor)
}

type faultCreateFormDepTx struct {
	storage.Transaction
	parent *faultCreateFormDepStore
}

func (t *faultCreateFormDepTx) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	t.parent.addDepCalls++
	if t.parent.addDepCalls == t.parent.failOnAddDep {
		return errInjectedCreateFormDepFailure
	}
	return t.Transaction.AddDependency(ctx, dep, actor)
}

// assertNoOrphanFormIssue fails if any issue with the given title survived a
// failed create — the pre-fix bug committed the issue before the edge failed.
func assertNoOrphanFormIssue(t *testing.T, real storage.DoltStorage, title string) {
	t.Helper()
	ctx := context.Background()
	issues, err := real.SearchIssues(ctx, title, types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	for _, iss := range issues {
		if iss.Title == title {
			t.Errorf("REGRESSION (14m3s): orphaned issue %s (%q) committed despite the edge write failing — a partial create-form create left a durable issue MISSING its declared edge", iss.ID, iss.Title)
		}
	}
}

// TestCreateFormWithDepIsAtomic_14m3s forces the --deps edge write to fail and
// asserts the form create is all-or-nothing: it returns an error and no orphan
// issue remains.
func TestCreateFormWithDepIsAtomic_14m3s(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// A pre-existing target the new issue will declare a dependency on.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-target", Title: "dep target", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	fault := &faultCreateFormDepStore{DoltStorage: real, failOnAddDep: 1}
	const title = "atomic dep form create 14m3s"
	fv := &createFormValues{
		Title:        title,
		IssueType:    string(types.TypeTask),
		Priority:     2,
		Dependencies: []string{"blocks:test-target"},
	}

	_, err := CreateIssueFromFormValues(ctx, fault, fv, "test-actor")
	if err == nil {
		t.Fatalf("expected a non-nil error when the --deps edge write fails; got nil (pre-fix bug returns (issue, nil) and drops the edge)")
	}
	assertNoOrphanFormIssue(t, real, title)
}

// TestCreateFormWithParentIsAtomic_14m3s forces the --parent parent-child edge
// write to fail and asserts the child issue is not left orphaned.
func TestCreateFormWithParentIsAtomic_14m3s(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// A parent to hang the child under. Use a plain open task so the closed-parent
	// guard (beads-3jdex) does not short-circuit before the create.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-parent", Title: "parent", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	fault := &faultCreateFormDepStore{DoltStorage: real, failOnAddDep: 1}
	const title = "atomic child form create 14m3s"
	fv := &createFormValues{
		Title:     title,
		IssueType: string(types.TypeTask),
		Priority:  2,
		ParentID:  "test-parent",
	}

	_, err := CreateIssueFromFormValues(ctx, fault, fv, "test-actor")
	if err == nil {
		t.Fatalf("expected a non-nil error when the parent-child edge write fails; got nil (pre-fix bug returns (issue, nil) and drops the edge)")
	}
	assertNoOrphanFormIssue(t, real, title)
}

// TestCreateFormAutogenDepTargetNotEmpty_14m3s is the HAPPY-PATH guard against
// the beads-1gvh4 class: a form create with --deps but NO --parent auto-generates
// the issue ID inside tx.CreateIssue, so building the edge struct BEFORE the tx
// (capturing the still-empty issue.ID) would store an edge with an empty
// endpoint. This asserts the successfully-created issue's edge carries the REAL
// minted ID, not "". (Fault-injection-only teeth pass with an empty id and miss
// this — the 1gvh4 lesson.)
func TestCreateFormAutogenDepTargetNotEmpty_14m3s(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-tgt", Title: "blocks target", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	const title = "autogen dep form create 14m3s"
	fv := &createFormValues{
		Title:        title,
		IssueType:    string(types.TypeTask),
		Priority:     2,
		Dependencies: []string{"blocks:test-tgt"}, // no --parent → auto-gen ID
	}

	issue, err := CreateIssueFromFormValues(ctx, real, fv, "test-actor")
	if err != nil {
		t.Fatalf("create-form failed: %v", err)
	}
	if issue.ID == "" {
		t.Fatalf("created issue has empty ID")
	}

	// create-form stores the edge as <newID> blocks test-tgt (no swapDirection),
	// so the edge lives on the NEW issue's dependency records.
	deps, err := real.GetDependencyRecords(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetDependencyRecords(%s): %v", issue.ID, err)
	}
	var found bool
	for _, d := range deps {
		if d.Type == types.DepBlocks && d.DependsOnID == "test-tgt" {
			if d.IssueID == "" {
				t.Errorf("REGRESSION (1gvh4 class): edge stored with EMPTY IssueID for auto-gen create-form create (blocks:test-tgt captured issue.ID before it was minted)")
			}
			if d.IssueID != issue.ID {
				t.Errorf("edge IssueID=%q, want minted %q", d.IssueID, issue.ID)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("REGRESSION (1gvh4 class): no blocks->test-tgt edge on %s — auto-gen create-form dropped/mis-endpointed the --deps edge", issue.ID)
	}
}
