//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestNoteRejectsWhitespaceOnly_4q09 is the end-to-end regression for
// beads-4q09: `bd note <id> "   "` (whitespace-only text) stored a blank note
// and exited 0, because note.go guarded with `noteText == ""` (bare-empty)
// rather than `strings.TrimSpace(noteText) == ""`. Its sibling `bd comment`
// already rejects whitespace-only input ("comment text cannot be empty"), so a
// whitespace note is an inconsistent accept of effectively-empty content. The
// fix TrimSpaces before the empty-check, mirroring comment.go. The guard sits
// ahead of the proxiedServer branch, so it covers both the direct and proxied
// paths.
func TestNoteRejectsWhitespaceOnly_4q09(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "nw")

	created := bdCreate(t, bd, dir, "whitespace note target", "--type", "task")

	// Precondition: a genuine note is accepted (exit 0).
	if out, err := bdRunWithFlockRetry(t, bd, dir, "note", created.ID, "a real note"); err != nil {
		t.Fatalf("precondition: bd note with real text should succeed: %v\n%s", err, out)
	}

	// Whitespace-only note text must be REJECTED (non-zero exit), matching the
	// sibling `bd comment` behavior — not silently stored as a blank note.
	out, err := bdRunWithFlockRetry(t, bd, dir, "note", created.ID, "   ")
	if err == nil {
		t.Fatalf("bd note with whitespace-only text should be rejected (non-zero exit); got success:\n%s", out)
	}
	if !strings.Contains(string(out), "note text is empty") {
		t.Fatalf("bd note whitespace-reject should report 'note text is empty'; got:\n%s", out)
	}
}
