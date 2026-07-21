//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedRenamePrefixAlnumSuffix_kzj4s proves `bd rename-prefix` rewrites
// text references whose id has a BASE36 (letter-bearing) suffix, not only
// digits-only sequential ids.
//
// beads-kzj4s: renamePrefixInDB's text-ref regex was
//   \b + QuoteMeta(oldPrefix) + `-(\d+)\b`
// — the `\d+` suffix matches DIGITS ONLY. Real bd ids are base36 hashes
// (e.g. op-16f), so every letter-bearing ref in description/design/notes/
// acceptance + the g8qfo comment-body rewrite (which reuses the SAME
// oldPrefixPattern) was silently skipped, leaving dangling old-prefix refs
// after a rename-prefix. The repair path (rename_prefix.go:355) already used the
// correct alnum suffix; the fix mirrors it: `-[a-z0-9]+`.
//
// ⚠️ Why this shipped green: the pre-existing rename-prefix / g8qfo tests used
// NUMERIC-suffix ids (cp-100 / cp-200), which the digits-only regex matched
// fine. These teeth use a LETTER-suffix ref so the mutant is exposed.
//
// MUTATION-VERIFY: revert oldPrefixPattern to `-(\d+)` and both subtests go RED
// — the letter-suffix ref (op-16f) stays as the OLD prefix in the description
// and the comment body (dangling ref).
func TestEmbeddedRenamePrefixAlnumSuffix_kzj4s(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A base36 letter-suffix id referenced in a TEXT FIELD (description) must be
	// rewritten to the new prefix by rename-prefix.
	t.Run("text_field_ref_with_letter_suffix_rewritten", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "op")
		// Target with a letter-bearing base36 suffix — the case the digits-only
		// regex silently dropped.
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "op-16f")
		bdCreate(t, bd, dir, "refs", "--type", "task", "--id", "op-ref",
			"-d", "blocked on op-16f before we ship")

		bdRenamePrefix(t, bd, dir, "np")

		// refs itself is renamed op-ref -> np-ref by the prefix rename.
		got := showIssue(t, bd, dir, "np-ref")
		if !strings.Contains(got.Description, "np-16f") {
			t.Errorf("beads-kzj4s: rename-prefix did not rewrite the letter-suffix ref op-16f -> np-16f in the description\n"+
				"got description: %q\nwant it to contain %q (digits-only regex skipped the base36 suffix)", got.Description, "np-16f")
		}
		if strings.Contains(got.Description, "op-16f") {
			t.Errorf("beads-kzj4s: rename-prefix left a DANGLING old-prefix ref op-16f in the description\n"+
				"got description: %q", got.Description)
		}
	})

	// The same oldPrefixPattern drives the g8qfo comment-body rewrite, so a
	// letter-suffix ref in a COMMENT must be rewritten too (g8qfo was inert for
	// alnum ids until this regex fix).
	t.Run("comment_body_ref_with_letter_suffix_rewritten", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cq")
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "cq-9ab")
		other := bdCreate(t, bd, dir, "other", "--type", "task", "--id", "cq-def")
		bdComment(t, bd, dir, other.ID, "depends on cq-9ab landing first")

		bdRenamePrefix(t, bd, dir, "mv")

		// other is renamed cq-def -> mv-def.
		got := bdCommentsShow(t, bd, dir, "mv-def")
		if !strings.Contains(got, "mv-9ab") {
			t.Errorf("beads-kzj4s: rename-prefix did not rewrite the letter-suffix ref cq-9ab -> mv-9ab in the comment body\n"+
				"comment output:\n%s\nwant it to contain %q (g8qfo comment rewrite inert for alnum until the regex fix)", got, "mv-9ab")
		}
		if strings.Contains(got, "cq-9ab") {
			t.Errorf("beads-kzj4s: rename-prefix left a DANGLING old-prefix ref cq-9ab in the comment body\n"+
				"comment output:\n%s", got)
		}
	})
}
