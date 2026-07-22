//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestRepairPrefixesRewritesCommentBody_siggb is the behavioral teeth for
// beads-siggb: `bd rename-prefix <new> --repair` (the multi-prefix
// CONSOLIDATION path, repairPrefixes) rewrote id references in the 5 issue text
// fields but NEVER visited the comments table. So a comment that referenced any
// repaired id — a cross-issue ref OR the row's own old id — kept the
// now-nonexistent id after consolidation: a dangling reference, reported with
// RC=0 and no warning.
//
// The sibling single-old-prefix path renamePrefixInDB already rewrites comment
// bodies via beads-g8qfo (rewriteCommentRefsInTx); repairPrefixes is the sibling
// the comment-body fixes (g8qfo rename / au6dv delete / k0yri self-ref) never
// reached. The fix adapts repairPrefixes's batch replaceFunc into the
// (string)->(string,bool) shape and calls rewriteCommentRefsInTx after
// UpdateIssueID in the repair tx loop, mirroring renamePrefixInDB.
//
// MUTATION-VERIFY: delete the rewriteCommentRefsInTx call in repairPrefixes and
// both subtests go RED — the comment bodies keep the vanished old ids.
func TestRepairPrefixesRewritesCommentBody_siggb(t *testing.T) {
	tmpDir := t.TempDir()
	// Target prefix is "kw"; the store is seeded with an "old"-prefix row set
	// too, giving detectPrefixes a multi-prefix corrupted DB to consolidate.
	real := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "kw")
	ctx := context.Background()

	// Set the global store so repairPrefixes's rewriteCommentRefsInTx (which
	// routes comment reads/writes through the tx handle) operates on this store.
	oldStore := store
	oldActor := actor
	store = real
	actor = "test"
	defer func() {
		store = oldStore
		actor = oldActor
	}()

	// A corrupted multi-prefix DB: target "kw" plus two wrong-prefix "old-" rows
	// that --repair will consolidate to kw-*.
	//   - old-root: references its OWN old id in a comment (self-ref, k0yri axis)
	//   - old-leaf: references old-root in a comment (cross-issue ref, g8qfo axis)
	// Both comment bodies must be rewritten to the NEW ids after --repair.
	seed := []types.Issue{
		{ID: "kw-keep", Title: "already correct", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "old-root", Title: "root", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "old-leaf", Title: "leaf", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	}
	for i := range seed {
		if err := real.CreateIssue(ctx, &seed[i], "test"); err != nil {
			t.Fatalf("failed to create %s: %v", seed[i].ID, err)
		}
	}
	// Seed via AddIssueComment (the comments TABLE that rewriteCommentRefsInTx /
	// GetIssueComments read) — NOT AddComment, which writes a comment EVENT.
	if _, err := real.AddIssueComment(ctx, "old-root", "test", "old-root supersedes prior scope; see old-root for context"); err != nil {
		t.Fatalf("add self-ref comment: %v", err)
	}
	if _, err := real.AddIssueComment(ctx, "old-leaf", "test", "blocked on old-root before we ship"); err != nil {
		t.Fatalf("add cross-ref comment: %v", err)
	}

	all, err := real.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	prefixes := detectPrefixes(all)
	if _, ok := prefixes["old"]; !ok {
		t.Fatalf("expected an 'old' prefix in the corrupted set, got %v", prefixes)
	}

	if err := repairPrefixes(ctx, real, "test", "kw", all, prefixes, false); err != nil {
		t.Fatalf("repair failed: %v", err)
	}

	// After consolidation the repaired rows carry the kw- prefix. Re-derive the
	// new ids from the surviving rows by their (unchanged) titles.
	after, err := real.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("search after repair: %v", err)
	}
	newIDByTitle := map[string]string{}
	for _, is := range after {
		newIDByTitle[is.Title] = is.ID
	}
	rootNewID := newIDByTitle["root"]
	leafNewID := newIDByTitle["leaf"]
	if rootNewID == "" || leafNewID == "" {
		t.Fatalf("could not locate repaired rows by title: %v", newIDByTitle)
	}
	if strings.HasPrefix(rootNewID, "old-") || strings.HasPrefix(leafNewID, "old-") {
		t.Fatalf("repair left rows on the old prefix: root=%s leaf=%s", rootNewID, leafNewID)
	}

	commentBody := func(t *testing.T, issueID string) string {
		t.Helper()
		cs, err := real.GetIssueComments(ctx, issueID)
		if err != nil {
			t.Fatalf("get comments for %s: %v", issueID, err)
		}
		var b strings.Builder
		for _, c := range cs {
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
		return b.String()
	}

	t.Run("self_reference_in_own_comment_rewritten", func(t *testing.T) {
		got := commentBody(t, rootNewID)
		if strings.Contains(got, "old-root") {
			t.Errorf("beads-siggb: --repair left a DANGLING self-reference old-root in the comment body of %s\n"+
				"comment body:\n%s", rootNewID, got)
		}
		if !strings.Contains(got, rootNewID) {
			t.Errorf("beads-siggb: --repair did not rewrite the self-reference old-root -> %s in the comment body\n"+
				"comment body:\n%s", rootNewID, got)
		}
	})

	t.Run("cross_reference_in_comment_rewritten", func(t *testing.T) {
		got := commentBody(t, leafNewID)
		if strings.Contains(got, "old-root") {
			t.Errorf("beads-siggb: --repair left a DANGLING cross-reference old-root in the comment body of %s\n"+
				"comment body:\n%s", leafNewID, got)
		}
		if !strings.Contains(got, rootNewID) {
			t.Errorf("beads-siggb: --repair did not rewrite the cross-reference old-root -> %s in the comment body\n"+
				"comment body:\n%s", rootNewID, got)
		}
	})
}
