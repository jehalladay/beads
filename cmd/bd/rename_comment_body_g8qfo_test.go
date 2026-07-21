//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedRenameRewritesCommentBody_g8qfo proves both `bd rename` and
// `bd rename-prefix` rewrite id references that live inside a COMMENT BODY, not
// only the 5 issue text fields (title/description/design/notes/acceptance).
//
// beads-g8qfo: the rename reference sweep visited only the issue table's text
// fields. A reference an author wrote inside a comment body ("see bd-abc for
// context") kept the OLD id after the rename and became a DANGLING ref to an id
// that no longer exists — silent, invisible until someone followed it. Both the
// rename help ("Labels, comments, and events") and the rename-prefix help
// ("all text references across all fields") promise comment refs are updated.
//
// The fix reuses the post-1nvr5 idReferenceRewriter, so it inherits the
// id-charclass token boundary and can NEVER re-introduce the sibling-prefix
// corruption 1nvr5 fixed: a comment saying "bd-abc-2" must be left alone when
// bd-abc is renamed.
//
// Teeth: create a target + a distinct sibling, comment a reference to each on a
// THIRD issue, rename, then read the comment body back via `bd comments`.
// Mutation-verify: drop the comment-rewrite pass and the standalone-ref subtest
// goes RED (dangling old id still present).
func TestEmbeddedRenameRewritesCommentBody_g8qfo(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// SINGULAR `bd rename`: a standalone ref in a comment body is rewritten; a
	// distinct hyphen-extended sibling id in the same comment is preserved.
	t.Run("rename_rewrites_standalone_ref_preserves_sibling", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cb")
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "cb-abc")
		refs := bdCreate(t, bd, dir, "refs", "--type", "task", "--id", "cb-ref")
		bdComment(t, bd, dir, refs.ID, "see cb-abc and also sibling cb-abc-2 for context")

		bdRename(t, bd, dir, "cb-abc", "cb-xyz")

		got := bdCommentsShow(t, bd, dir, refs.ID)
		if !strings.Contains(got, "see cb-xyz") {
			t.Errorf("bd rename did not rewrite the standalone id ref in a comment body\n"+
				"comment output:\n%s\nwant it to contain %q [beads-g8qfo]", got, "see cb-xyz")
		}
		if !strings.Contains(got, "cb-abc-2") {
			t.Errorf("bd rename CORRUPTED a distinct sibling id (cb-abc-2) inside a comment body\n"+
				"comment output:\n%s\nsibling cb-abc-2 must be preserved [beads-g8qfo/1nvr5]", got)
		}
		if strings.Contains(got, "see cb-abc ") || strings.Contains(got, "see cb-abc\n") {
			t.Errorf("bd rename left a DANGLING old ref (cb-abc) in a comment body\n"+
				"comment output:\n%s [beads-g8qfo]", got)
		}
	})

	// PREFIX `bd rename-prefix`: comment-body refs across the whole DB get the
	// prefix rewrite too (help promises "all text references across all fields").
	// rename-prefix's text rewrite matches oldPrefix-(\d+), so use numeric-suffix
	// ids; the comment-rewrite pass reuses that same prefix pattern.
	t.Run("rename_prefix_rewrites_comment_body_ref", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cp")
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "cp-100")
		other := bdCreate(t, bd, dir, "other", "--type", "task", "--id", "cp-200")
		bdComment(t, bd, dir, other.ID, "depends on cp-100 landing first")

		bdRenamePrefix(t, bd, dir, "renamed")

		// other issue id is now renamed-200 after the prefix rename.
		got := bdCommentsShow(t, bd, dir, "renamed-200")
		if !strings.Contains(got, "renamed-100") {
			t.Errorf("bd rename-prefix did not rewrite the comment-body id ref\n"+
				"comment output:\n%s\nwant it to contain %q [beads-g8qfo]", got, "renamed-100")
		}
		if strings.Contains(got, "cp-100") {
			t.Errorf("bd rename-prefix left a DANGLING old-prefix ref (cp-100) in a comment body\n"+
				"comment output:\n%s [beads-g8qfo]", got)
		}
	})
}
