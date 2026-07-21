//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-k0yri: `bd rename <old> <new>` rewrites cross-issue references (uorhi)
// but LEFT the renamed issue's OWN body dangling. UpdateIssueID re-keys the row
// and writes its text fields VERBATIM from the pre-fetched issue, and the
// reference sweep then `continue`d on issue.ID == newID ("Skip the renamed issue
// itself") — so a body that referenced its own old id ("this issue oldID
// supersedes ...") kept the now-nonexistent oldID: a dangling self-reference,
// reported with RC=0 and no warning. Both the direct (rename.go) and proxied
// (rename_proxied_server.go) legs skipped identically — a shared blind spot.
//
// The fix removes the skip so the renamed row is swept like any other
// referencing issue. The sweep's SearchIssues runs AFTER the re-key, so the row
// appears as newID with an old-id-containing body; the shared idReferenceRewriter
// (oldID->newID) fixes the self-ref. The newID token is id-char-bounded, so
// re-visiting an already-rewritten row is idempotent (no double-apply).
//
// MUTATION-VERIFY: restore the `if issue.ID == newID { continue }` skip in
// updateReferencesInAllIssuesTx and this test FAILS — the renamed row's
// description + comment still textually reference the vanished old id.
func TestRenameRewritesOwnSelfReference_k0yri(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// An issue whose own description references its own id, plus a self-referencing
	// comment body — both are user-authored ref sites (g8qfo class).
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-abc", Title: "root", Status: types.StatusOpen,
		Description: "this issue test-abc supersedes prior scope; see test-abc for context",
		Priority:    1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := real.AddIssueComment(ctx, "test-abc", "test", "cross-check against test-abc before closing"); err != nil {
		t.Fatalf("add comment: %v", err)
	}

	// Happy-path rename (no fault): empty failIssueID never matches.
	if err := runRenameWithFault(t, real, "test-abc", "test-xyz", ""); err != nil {
		t.Fatalf("rename failed: %v", err)
	}

	renamed, gerr := real.GetIssue(ctx, "test-xyz")
	if gerr != nil {
		t.Fatalf("renamed id test-xyz does not resolve: %v", gerr)
	}

	// The renamed row's own description must now reference the NEW id, not the
	// vanished old id. Under the pre-fix skip this stays "...test-abc...".
	if want := "this issue test-xyz supersedes prior scope; see test-xyz for context"; renamed.Description != want {
		t.Errorf("REGRESSION (k0yri): renamed issue's own description was not rewritten — dangling self-reference:\n got %q\nwant %q", renamed.Description, want)
	}

	// The renamed row's own comment body must likewise be rewritten.
	comments, cerr := real.GetIssueComments(ctx, "test-xyz")
	if cerr != nil {
		t.Fatalf("get comments: %v", cerr)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment on test-xyz, got %d", len(comments))
	}
	if want := "cross-check against test-xyz before closing"; comments[0].Text != want {
		t.Errorf("REGRESSION (k0yri): renamed issue's own comment body was not rewritten — dangling self-reference:\n got %q\nwant %q", comments[0].Text, want)
	}
}
