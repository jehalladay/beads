//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedUpdateAppendNotesDedup_qh4dy is the DIRECT-path teeth for
// beads-qh4dy: a repeated issue id in ONE `bd update` batch that carries the
// NON-IDEMPOTENT --append-notes flag must append the note EXACTLY once.
//
// Without the uniqueStrings(args) dedup at the command entry (update.go,
// mirroring delete.go:86 + beads-hzg2y label add/remove), `bd update X X
// --append-notes foo` iterates args and calls issueStore.AppendNotes — a single
// server-side CONCAT_WS — once per occurrence of X. So X's note body gets "foo"
// appended TWICE: real data duplication, unlike the idempotent close/label
// writes hzg2y covered (which only over-COUNT the reported result while the DB
// stays correct). beads-1d32 added a pre-resolve guard (update.go:497) against
// the retry-after-partial-fail double-append, but it resolves each id without
// deduping, so the in-batch dup slips through.
//
// The dedup keeps the first occurrence, so a repeated id appends once; genuinely
// distinct ids in a batch each still get the note (negative control). This is
// the DIRECT half; the proxied twin is covered by
// update_appendnotes_dedup_proxied_qh4dy_test.go (same shared fix, since the
// dedup lives before the usesProxiedServer() dispatch).
//
// MUTATION-VERIFY: remove `args = uniqueStrings(args)` from update.go and
// append_repeated_id_appends_once goes RED (notes contain the marker TWICE); the
// distinct-ids control stays GREEN. Restore → all GREEN.
func TestEmbeddedUpdateAppendNotesDedup_qh4dy(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "qh")

	t.Run("append_repeated_id_appends_once", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Append dup id", "--type", "task")
		const marker = "QH4DY-MARKER-ONCE"
		bdUpdate(t, bd, dir, issue.ID, issue.ID, "--append-notes", marker)

		got := bdShow(t, bd, dir, issue.ID)
		if n := strings.Count(got.Notes, marker); n != 1 {
			t.Fatalf("expected the note appended exactly once for a repeated id, "+
				"got %d occurrences of %q in notes=%q", n, marker, got.Notes)
		}
	})

	t.Run("append_distinct_ids_each_appended_once", func(t *testing.T) {
		// Negative control: two DIFFERENT ids in one batch must each get the note.
		i1 := bdCreate(t, bd, dir, "Append distinct 1", "--type", "task")
		i2 := bdCreate(t, bd, dir, "Append distinct 2", "--type", "task")
		const marker = "QH4DY-DISTINCT"
		bdUpdate(t, bd, dir, i1.ID, i2.ID, "--append-notes", marker)

		g1 := bdShow(t, bd, dir, i1.ID)
		g2 := bdShow(t, bd, dir, i2.ID)
		if n := strings.Count(g1.Notes, marker); n != 1 {
			t.Errorf("expected %s to carry the note once, got %d in notes=%q", i1.ID, n, g1.Notes)
		}
		if n := strings.Count(g2.Notes, marker); n != 1 {
			t.Errorf("expected %s to carry the note once, got %d in notes=%q", i2.ID, n, g2.Notes)
		}
	})
}
