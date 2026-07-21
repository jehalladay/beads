//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedDeleteRewritesCommentBody_au6dv proves `bd delete` tombstones id
// references that live inside a COMMENT BODY on a connected issue, not only the
// 5 issue text fields (title/description/design/notes/acceptance).
//
// beads-au6dv: the delete reference cleanup visited only the connected issue's
// text fields. A reference an author wrote inside a comment body ("see db-abc
// for context") kept the LIVE id after the referenced issue was deleted and
// became a DANGLING ref to an id that no longer exists — the exact gap g8qfo
// just closed for rename. delete rewrites the ref into the "[deleted:id]"
// tombstone (like the field rewrites), reusing the shared rewriteCommentRefs
// helper + the beads-36d6n loop-to-fixed-point tombstone rewriter, so it also
// inherits the id-charclass token boundary (1nvr5): a comment saying "db-abc-2"
// must be left alone when db-abc is deleted.
//
// Teeth: create a target + a distinct sibling, comment a reference to each on a
// THIRD connected issue, delete the target, then read the comment body back via
// `bd comments`. Mutation-verify: drop the comment-rewrite pass and the ref
// stays the live old id → RED.
func TestEmbeddedDeleteRewritesCommentBody_au6dv(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A standalone ref in a connected issue's comment body is tombstoned on
	// delete; a distinct hyphen-extended sibling id in the same comment is
	// preserved (the 1nvr5 boundary invariant, inherited via the rewriter).
	t.Run("delete_tombstones_comment_ref_preserves_sibling", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "db")
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "db-abc")
		bdCreate(t, bd, dir, "sibling", "--type", "task", "--id", "db-abc-2")
		refs := bdCreate(t, bd, dir, "refs", "--type", "task", "--id", "db-ref")
		// Make refs a CONNECTED issue so the delete reference sweep visits it.
		bdDepAdd(t, bd, dir, refs.ID, "db-abc", "--type", "related")
		bdComment(t, bd, dir, refs.ID, "see db-abc and also sibling db-abc-2 for context")

		// --force skips the interactive preview and performs the delete.
		bdDelete(t, bd, dir, "db-abc", "--force")

		got := bdCommentsShow(t, bd, dir, refs.ID)
		if !strings.Contains(got, "[deleted:db-abc]") {
			t.Errorf("bd delete did not tombstone the id ref in a comment body\n"+
				"comment output:\n%s\nwant it to contain %q [beads-au6dv]", got, "[deleted:db-abc]")
		}
		if !strings.Contains(got, "db-abc-2") {
			t.Errorf("bd delete CORRUPTED a distinct sibling id (db-abc-2) inside a comment body\n"+
				"comment output:\n%s\nsibling db-abc-2 must be preserved [beads-au6dv/1nvr5]", got)
		}
		// The live old id must no longer stand alone (it survives only inside the
		// tombstone token and the sibling db-abc-2).
		if strings.Contains(got, "see db-abc ") {
			t.Errorf("bd delete left a DANGLING live ref (db-abc) in a comment body\n"+
				"comment output:\n%s [beads-au6dv]", got)
		}
	})

	// Two adjacent refs sharing one delimiter inside a comment: BOTH tombstoned
	// (the 36d6n adjacent-run loop, exercised through the comment path).
	t.Run("delete_tombstones_adjacent_comment_refs", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "dc")
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "dc-abc")
		refs := bdCreate(t, bd, dir, "refs", "--type", "task", "--id", "dc-ref")
		bdDepAdd(t, bd, dir, refs.ID, "dc-abc", "--type", "related")
		bdComment(t, bd, dir, refs.ID, "dupes: dc-abc dc-abc")

		bdDelete(t, bd, dir, "dc-abc", "--force")

		got := bdCommentsShow(t, bd, dir, refs.ID)
		if strings.Count(got, "[deleted:dc-abc]") < 2 {
			t.Errorf("bd delete rewrote only the FIRST of two adjacent comment refs\n"+
				"comment output:\n%s\nwant two [deleted:dc-abc] tombstones [beads-au6dv/36d6n]", got)
		}
	})
}
