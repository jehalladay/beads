//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedUpdateAppendNotesDedup_qh4dy is the proxied-server twin of
// beads-qh4dy. Hub-connected crew route `bd update` through
// runUpdateProxiedServer, whose mutation loop (update_proxied_server.go) calls
// AppendNotes once per arg — so `bd update X X --append-notes foo` double-appends
// the same note on the proxied path exactly as it does on the direct path.
//
// The uniqueStrings(args) dedup lives at the SHARED command entry (update.go),
// BEFORE the usesProxiedServer() branch, so the one fix covers both paths. This
// test asserts proxied parity with the direct teeth: a repeated id appends once,
// two distinct ids each get the note.
//
// MUTATION-VERIFY: remove `args = uniqueStrings(args)` from update.go →
// proxied_append_repeated_id_appends_once goes RED (notes carry the marker
// twice). Restore → GREEN.
func TestProxiedUpdateAppendNotesDedup_qh4dy(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("proxied_append_repeated_id_appends_once", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "qhpz")
		a := bdProxiedCreate(t, bd, p.dir, "Proxied append dup", "--type", "task")
		const marker = "QH4DY-PROXY-ONCE"
		out, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, a.ID, "--append-notes", marker)
		if err != nil {
			t.Fatalf("proxied update append dup failed: %v\n%s", err, out)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if n := strings.Count(got.Notes, marker); n != 1 {
			t.Fatalf("expected the note appended exactly once for a repeated id, "+
				"got %d occurrences of %q in notes=%q", n, marker, got.Notes)
		}
	})

	t.Run("proxied_append_distinct_ids_each_appended_once", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "qhpd")
		i1 := bdProxiedCreate(t, bd, p.dir, "Proxied distinct 1", "--type", "task")
		i2 := bdProxiedCreate(t, bd, p.dir, "Proxied distinct 2", "--type", "task")
		const marker = "QH4DY-PROXY-DISTINCT"
		out, err := bdProxiedRun(t, bd, p.dir, "update", i1.ID, i2.ID, "--append-notes", marker)
		if err != nil {
			t.Fatalf("proxied update distinct ids failed: %v\n%s", err, out)
		}
		g1 := bdProxiedShow(t, bd, p.dir, i1.ID)
		g2 := bdProxiedShow(t, bd, p.dir, i2.ID)
		if n := strings.Count(g1.Notes, marker); n != 1 {
			t.Errorf("expected %s to carry the note once, got %d in notes=%q", i1.ID, n, g1.Notes)
		}
		if n := strings.Count(g2.Notes, marker); n != 1 {
			t.Errorf("expected %s to carry the note once, got %d in notes=%q", i2.ID, n, g2.Notes)
		}
	})
}
