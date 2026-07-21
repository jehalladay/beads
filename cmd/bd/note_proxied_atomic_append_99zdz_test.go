//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-99zdz (proxied leg): the PROXIED `bd note` path (note_proxied_server.go,
// runNoteProxiedServer) previously appended via a client-side read-modify-write —
// GetIssue → concat in Go → whole-blob write into UpdateSpec.Fields["notes"] via
// ApplyUpdate. Two concurrent proxied `bd note` on the same issue (which genuinely
// interleave on a shared hub sql-server) both based on the same snapshot → last-
// writer-wins → one note lost. The fix carries the append as an atomic server-side
// op on the spec (AppendNotes + HasAppendNotes → ApplyUpdate → issueRepo.AppendNotes,
// a single CONCAT_WS), mirroring bd update --append-notes (beads-jscve). This test
// drives the real proxied-server subprocess bd end-to-end and proves a `bd note`
// append preserves the pre-existing notes and newline-separates the new note.
func TestProxiedServerNoteAppend_99zdz(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	p := bdProxiedInit(t, bd, "pna")

	// Seed an issue with an existing note so an append that clobbers (rather than
	// concatenates) would drop it.
	issue := bdProxiedCreate(t, bd, p.dir, "Proxied note target", "--notes", "first line")

	if _, err := bdProxiedRun(t, bd, p.dir, "note", issue.ID, "second line"); err != nil {
		t.Fatalf("bd note (proxied) failed: %v", err)
	}

	got := bdProxiedShow(t, bd, p.dir, issue.ID)
	if got.Notes != "first line\nsecond line" {
		t.Fatalf("proxied bd note should append newline-separated, preserving existing notes; got %q", got.Notes)
	}

	// A second append separates again and keeps both prior lines — proves the
	// server-side append is order-preserving and non-clobbering across repeated
	// appends (the property lost under the client-side RMW when appends race).
	if _, err := bdProxiedRun(t, bd, p.dir, "note", issue.ID, "third line"); err != nil {
		t.Fatalf("bd note (proxied) 2 failed: %v", err)
	}
	got = bdProxiedShow(t, bd, p.dir, issue.ID)
	for _, want := range []string{"first line", "second line", "third line"} {
		if !strings.Contains(got.Notes, want) {
			t.Fatalf("proxied bd note lost %q across repeated appends; notes=%q", want, got.Notes)
		}
	}
	if got.Notes != "first line\nsecond line\nthird line" {
		t.Fatalf("expected exact newline-separated order; got %q", got.Notes)
	}
}
