//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestUpdateAppendNotesRejectsWhitespaceOnly_beln6 is the end-to-end regression
// for beads-beln6, the twin of beads-4q09 (bd note). `bd update <id>
// --append-notes "   "` (whitespace-only) appended a blank line to notes and
// exited 0, because the flag-read site stored the raw value with no empty-check
// — unlike --title, which TrimSpace-rejects. The fix rejects an all-whitespace
// append at both flag-read sites (update.go direct + update_input.go proxied),
// preserving the raw value for genuine notes. This test drives the real bd
// binary in DIRECT mode (the embedded harness runs no proxied server).
func TestUpdateAppendNotesRejectsWhitespaceOnly_beln6(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "an")

	created := bdCreate(t, bd, dir, "append-notes target", "--type", "task")

	// Precondition: a genuine --append-notes is accepted (exit 0).
	if out, err := bdRunWithFlockRetry(t, bd, dir, "update", created.ID, "--append-notes", "a real note"); err != nil {
		t.Fatalf("precondition: --append-notes with real text should succeed: %v\n%s", err, out)
	}

	// Whitespace-only append text must be REJECTED (non-zero exit), not silently
	// stored as a blank line.
	out, err := bdRunWithFlockRetry(t, bd, dir, "update", created.ID, "--append-notes", "   ")
	if err == nil {
		t.Fatalf("--append-notes with whitespace-only text should be rejected (non-zero exit); got success:\n%s", out)
	}
	if !strings.Contains(string(out), "append-notes text cannot be empty") {
		t.Fatalf("--append-notes whitespace-reject should report 'append-notes text cannot be empty'; got:\n%s", out)
	}

	// The rejected append must NOT have modified notes: the stored notes must
	// still be exactly the genuine note from the precondition (no trailing blank
	// line appended).
	got := bdShow(t, bd, dir, created.ID)
	if strings.TrimSpace(got.Notes) != "a real note" {
		t.Fatalf("rejected whitespace append must leave notes unchanged; got notes = %q", got.Notes)
	}
}
