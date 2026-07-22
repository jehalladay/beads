//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-vbm9s: `bd rename-prefix <new> --repair` (the multi-prefix
// consolidation branch, repairPrefixes) rewrote the 5 issue text fields but
// NEVER visited the comments table — the exact g8qfo defect, on the sibling
// path g8qfo's fix skipped. g8qfo wired rewriteCommentRefsInTx into the normal
// renamePrefixInDB path only. So consolidating a corrupted multi-prefix DB
// (the very thing --repair exists to CURE) left comment bodies pointing at the
// vanished old ids — a dangling ref, RC=0, no warning.
//
// The fix calls rewriteCommentRefsInTx after each UpdateIssueID in
// repairPrefixes, reusing the repair path's OWN renameMap-keyed rewrite (the
// generic oldPrefixPattern + replaceFunc), because --repair consolidates
// multiple prefixes at once — a single-prefix rewrite would miss the others.
//
// A corrupted multi-prefix DB is planted at the store layer (CreateIssue takes
// explicit off-prefix ids, the same technique TestRepairMultiplePrefixes uses),
// then repairPrefixes is driven directly. One issue's comment references
// another issue by its OLD id, plus a hyphen-extended sibling (old-2-x) that
// must be preserved (id-charclass token boundary).
//
// MUTATION-VERIFY: delete the rewriteCommentRefsInTx block from repairPrefixes
// and this test FAILS — the renamed issue's comment body still textually
// references the vanished old id.
func TestRepairPrefixRewritesCommentBody_vbm9s(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.SetConfig(ctx, "issue_prefix", "cp"); err != nil {
		t.Fatalf("set prefix: %v", err)
	}

	// A corrupted multi-prefix DB: one correct-prefix issue + two off-prefix
	// ("old-") issues, one of which carries a comment referencing the other by
	// its OLD id (plus a hyphen-extended sibling that must survive).
	seed := []types.Issue{
		{ID: "cp-100", Title: "correct", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "old-1", Title: "referer", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "old-2", Title: "referee", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	}
	for i := range seed {
		if err := real.CreateIssue(ctx, &seed[i], "test"); err != nil {
			t.Fatalf("create %s: %v", seed[i].ID, err)
		}
	}
	if _, err := real.AddIssueComment(ctx, "old-1", "test", "see old-2 for context; not the sibling old-2-x"); err != nil {
		t.Fatalf("add comment: %v", err)
	}

	allIssues, err := real.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("search issues: %v", err)
	}
	prefixes := detectPrefixes(allIssues)
	if len(prefixes) != 2 {
		t.Fatalf("expected 2 prefixes (cp, old), got %d: %v", len(prefixes), prefixes)
	}

	if err := repairPrefixes(ctx, real, "test", "cp", allIssues, prefixes, false); err != nil {
		t.Fatalf("repair failed: %v", err)
	}

	// repairPrefixes mints fresh hash ids, so discover the new ids by title.
	after, err := real.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("search after repair: %v", err)
	}
	var refererID, refereeID string
	for _, is := range after {
		switch is.Title {
		case "referer":
			refererID = is.ID
		case "referee":
			refereeID = is.ID
		}
	}
	if refererID == "" || refereeID == "" {
		t.Fatalf("could not locate renamed issues by title: referer=%q referee=%q", refererID, refereeID)
	}
	if !strings.HasPrefix(refererID, "cp-") || !strings.HasPrefix(refereeID, "cp-") {
		t.Fatalf("renamed ids do not carry the target prefix: referer=%q referee=%q", refererID, refereeID)
	}

	comments, err := real.GetIssueComments(ctx, refererID)
	if err != nil {
		t.Fatalf("get comments for %s: %v", refererID, err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment on %s, got %d", refererID, len(comments))
	}
	body := comments[0].Text

	// The dangling old ref must be gone, rewritten to the referee's NEW id.
	if strings.Contains(body, "see old-2 ") || strings.Contains(body, "old-2;") {
		t.Errorf("REGRESSION (vbm9s): --repair left a DANGLING old-prefix ref (old-2) in a comment body:\n%q [beads-vbm9s]", body)
	}
	if !strings.Contains(body, refereeID) {
		t.Errorf("REGRESSION (vbm9s): --repair did not rewrite the comment-body ref to the referee's new id %q:\n%q [beads-vbm9s]", refereeID, body)
	}
	// The extended sibling token must be preserved (never over-rewritten).
	if !strings.Contains(body, "old-2-x") {
		t.Errorf("REGRESSION (vbm9s/1nvr5): --repair corrupted the distinct sibling id old-2-x in a comment body:\n%q", body)
	}
}
