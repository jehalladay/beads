package dolt

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// These tests pin the finalizeUpdatedIssue umbrella (beads-2p6x): UpdateIssueInTx
// must re-run the create-path finalization (full Issue.ValidateWithCustom +
// ComputeContentHash) on the merged post-update issue, so the shared write path
// enforces the same invariants create does. Each subtest is a teeth-check for a
// child bug — reverting the finalize step turns it RED.
//
// Children:
//   beads-25k6: title (required, <=500) + estimated_minutes (>=0) invariants
//   beads-lsbu: metadata must be well-formed JSON object (schema)
//   beads-rzx8: content_hash must be recomputed after a content change

func newRegularIssue(t *testing.T, store *DoltStore, title string) string {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()
	iss := &types.Issue{Title: title, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	return iss.ID
}

// beads-25k6: empty / over-500 title must be rejected on the shared update path.
func TestUpdateFinalize_TitleInvariant(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	id := newRegularIssue(t, store, "valid title")

	if err := store.UpdateIssue(ctx, id, map[string]interface{}{"title": ""}, "tester"); err == nil {
		t.Error("empty title update should be rejected (25k6)")
	}
	if err := store.UpdateIssue(ctx, id, map[string]interface{}{"title": strings.Repeat("x", 501)}, "tester"); err == nil {
		t.Error("over-500 title update should be rejected (25k6)")
	}
	// A valid title change still succeeds.
	if err := store.UpdateIssue(ctx, id, map[string]interface{}{"title": "fine"}, "tester"); err != nil {
		t.Errorf("valid title update should succeed: %v", err)
	}
}

// beads-25k6: negative estimated_minutes must be rejected.
func TestUpdateFinalize_EstimateInvariant(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	id := newRegularIssue(t, store, "est test")
	neg := -5
	if err := store.UpdateIssue(ctx, id, map[string]interface{}{"estimated_minutes": neg}, "tester"); err == nil {
		t.Error("negative estimated_minutes update should be rejected (25k6)")
	}
	pos := 30
	if err := store.UpdateIssue(ctx, id, map[string]interface{}{"estimated_minutes": pos}, "tester"); err != nil {
		t.Errorf("valid estimate update should succeed: %v", err)
	}
}

// beads-lsbu: non-object metadata must be rejected on the shared update path.
func TestUpdateFinalize_MetadataObjectInvariant(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	id := newRegularIssue(t, store, "meta test")
	// A JSON array/scalar is valid JSON but not an object — must be rejected.
	if err := store.UpdateIssue(ctx, id, map[string]interface{}{"metadata": "[1,2,3]"}, "tester"); err == nil {
		t.Error("non-object (array) metadata update should be rejected (lsbu)")
	}
	if err := store.UpdateIssue(ctx, id, map[string]interface{}{"metadata": `{"k":"v"}`}, "tester"); err != nil {
		t.Errorf("valid object metadata update should succeed: %v", err)
	}
}

// beads-rzx8: content_hash must be recomputed when content changes.
func TestUpdateFinalize_ContentHashRecomputed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	id := newRegularIssue(t, store, "hash test original")
	before, err := store.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}
	if before.ContentHash == "" {
		t.Fatal("precondition: created issue should have a content_hash")
	}

	if err := store.UpdateIssue(ctx, id, map[string]interface{}{"title": "hash test CHANGED"}, "tester"); err != nil {
		t.Fatalf("update title: %v", err)
	}
	after, err := store.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.ContentHash == before.ContentHash {
		t.Error("content_hash should change after a title edit (rzx8)")
	}
	if after.ContentHash != after.ComputeContentHash() {
		t.Errorf("stored content_hash %q != recomputed %q (rzx8)", after.ContentHash, after.ComputeContentHash())
	}
}
