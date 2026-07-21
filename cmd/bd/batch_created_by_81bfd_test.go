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

// runBatchCreateCapturingID runs a single-op batch create script and returns the
// auto-generated issue ID (runBatchOp reports it as result.Target), so tests
// need not hard-code the minted prefix/counter.
func runBatchCreateCapturingID(t *testing.T, ctx context.Context, st storage.DoltStorage, script string) string {
	t.Helper()
	ops, err := parseBatchScript(strings.NewReader(script))
	if err != nil {
		t.Fatalf("parseBatchScript: %v", err)
	}
	var id string
	if err := st.RunInTransaction(ctx, "test: bd batch create", func(tx storage.Transaction) error {
		for _, op := range ops {
			res, rerr := runBatchOp(ctx, tx, op, "")
			if rerr != nil {
				return rerr
			}
			if res.Target != "" {
				id = res.Target
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("batch run: %v", err)
	}
	if id == "" {
		t.Fatalf("batch create produced no target ID")
	}
	return id
}

// TestBatch_CreateStampsCreatedByActor guards beads-81bfd: the batch create leg
// builds a types.Issue with no CreatedBy, so before the shared-seam backfill it
// landed created_by='' while `bd create` stamps the resolved actor. The seam
// (issueops.CreateIssueInTxWithResult) now falls back CreatedBy=actor when the
// caller left it empty — empty-only, mirroring dependencyCreatedBy.
func TestBatch_CreateStampsCreatedByActor(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbc")
	ctx := context.Background()

	// getActor() reads the global `actor` when cmdCtx is nil (test mode), which
	// is exactly what runBatchOp threads into tx.CreateIssue as actorName.
	prev := actor
	actor = "alice"
	t.Cleanup(func() { actor = prev })

	id := runBatchCreateCapturingID(t, ctx, st, "create task 2 batch provenance\n")

	got, err := st.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("GetIssue %s: %v", id, err)
	}
	if got.CreatedBy != "alice" {
		t.Errorf("batch-created created_by = %q, want %q (provenance dropped — beads-81bfd)", got.CreatedBy, "alice")
	}
}

// TestBatch_CreateParityWithDirectCreate asserts a batch-created and a
// store-seam-created issue carry identical, non-empty created_by for the same
// actor — the loop→batch parity axis from beads-81bfd.
func TestBatch_CreateParityWithDirectCreate(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbp")
	ctx := context.Background()

	prev := actor
	actor = "carol"
	t.Cleanup(func() { actor = prev })

	// Direct store create (mirrors what cmd/bd/create.go drives) with an empty
	// CreatedBy relies on the same seam fallback.
	direct := &types.Issue{
		Title:     "direct create",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := st.CreateIssue(ctx, direct, "carol"); err != nil {
		t.Fatalf("direct CreateIssue: %v", err)
	}

	batchedID := runBatchCreateCapturingID(t, ctx, st, "create task 2 batch create\n")
	batched, err := st.GetIssue(ctx, batchedID)
	if err != nil {
		t.Fatalf("GetIssue %s: %v", batchedID, err)
	}

	gotDirect, err := st.GetIssue(ctx, direct.ID)
	if err != nil {
		t.Fatalf("GetIssue %s: %v", direct.ID, err)
	}
	if gotDirect.CreatedBy == "" {
		t.Errorf("direct created_by is empty, want %q", "carol")
	}
	if batched.CreatedBy != gotDirect.CreatedBy {
		t.Errorf("created_by parity broken: batch=%q direct=%q", batched.CreatedBy, gotDirect.CreatedBy)
	}
}

// TestBatch_CreatePreservesSuppliedCreatedBy guards the empty-only invariant:
// an import/restore-supplied CreatedBy must be preserved, never overwritten by
// the resolving actor (matches dependencyCreatedBy semantics).
func TestBatch_CreatePreservesSuppliedCreatedBy(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "tbi")
	ctx := context.Background()

	prev := actor
	actor = "importer"
	t.Cleanup(func() { actor = prev })

	issue := &types.Issue{
		ID:        "tbi-1",
		Title:     "imported issue",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		CreatedBy: "original_author",
	}
	if err := st.CreateIssue(ctx, issue, "importer"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	got, err := st.GetIssue(ctx, "tbi-1")
	if err != nil {
		t.Fatalf("GetIssue tbi-1: %v", err)
	}
	if got.CreatedBy != "original_author" {
		t.Errorf("supplied created_by = %q, want %q (empty-only fallback overwrote an import value)", got.CreatedBy, "original_author")
	}
}
