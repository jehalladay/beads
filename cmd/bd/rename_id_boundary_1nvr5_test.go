//go:build cgo

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestEmbeddedRenameIDBoundary_1nvr5 proves `bd rename` rewrites a text
// reference to the renamed id ONLY when it is a standalone id token — never
// inside a hyphen-extended sibling id.
//
// beads-1nvr5: the reference rewriter used `\b` + oldID + `\b`. Go's \b word
// boundary treats '-' as a NON-word char, so `\bbd-abc\b` matched at the hyphen
// INSIDE a different, hyphen-extended sibling id like bd-abc-2 — silently
// rewriting bd-abc-2 -> bd-xyz-2 and corrupting a reference to an unrelated
// issue. The fix (shared idReferenceRewriter, used by the direct and proxied
// paths) defines an id token by the charclass [A-Za-z0-9_-] and re-emits the
// surrounding delimiters, matching the proven in-tree pattern in delete.go.
func TestEmbeddedRenameIDBoundary_1nvr5(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// TEST 1+2+3: sibling preserved, standalone rewritten, trailing-word-char
	// preserved — all in ONE reference field so the token discrimination is
	// exercised on a single rename.
	t.Run("sibling_id_preserved_standalone_rewritten", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rb")
		// The renamed target.
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "rb-abc")
		// An issue whose description references: a distinct sibling (rb-abc-2),
		// the standalone token (rb-abc), and a trailing-word-char id (rb-abcx).
		refs := bdCreate(t, bd, dir, "refs", "--type", "task", "--id", "rb-ref",
			"-d", "relates to rb-abc-2 and also rb-abc and rb-abcx")

		bdRename(t, bd, dir, "rb-abc", "rb-xyz")

		got := showIssue(t, bd, dir, refs.ID)
		want := "relates to rb-abc-2 and also rb-xyz and rb-abcx"
		if got.Description != want {
			t.Errorf("rename reference rewrite corrupted sibling/trailing ids\n got: %q\nwant: %q\n"+
				"(rb-abc-2 is a DISTINCT id and must NOT change; rb-abcx has a trailing word char; only the standalone rb-abc rewrites) [beads-1nvr5]",
				got.Description, want)
		}
	})

	// TEST 4: leading side — a preceding id-char means we're inside a longer
	// token, so it must NOT rewrite.
	t.Run("leading_id_char_preserved", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rl")
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "rl-abc")
		refs := bdCreate(t, bd, dir, "refs", "--type", "task", "--id", "rl-ref",
			"-d", "prefixed x-rl-abc and 9rl-abc and bare rl-abc here")

		bdRename(t, bd, dir, "rl-abc", "rl-xyz")

		got := showIssue(t, bd, dir, refs.ID)
		want := "prefixed x-rl-abc and 9rl-abc and bare rl-xyz here"
		if got.Description != want {
			t.Errorf("rename rewrote a reference where the id was NOT a standalone token\n got: %q\nwant: %q [beads-1nvr5]",
				got.Description, want)
		}
	})

	// TEST 5: field parity — title/design/notes/acceptance_criteria all get the
	// same boundary treatment (loop parity), and the sibling is preserved in
	// every one.
	t.Run("all_text_fields_get_boundary_treatment", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rf")
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "rf-abc")
		body := "rf-abc plus rf-abc-2"
		refs := bdCreate(t, bd, dir, "rf-abc header", "--type", "task", "--id", "rf-ref",
			"-d", body, "--design", body, "--notes", body, "--acceptance", body)

		bdRename(t, bd, dir, "rf-abc", "rf-xyz")

		got := showIssue(t, bd, dir, refs.ID)
		wantBody := "rf-xyz plus rf-abc-2" // standalone rewritten, sibling intact
		wantTitle := "rf-xyz header"
		if got.Title != wantTitle {
			t.Errorf("title: got %q want %q [beads-1nvr5]", got.Title, wantTitle)
		}
		if got.Description != wantBody {
			t.Errorf("description: got %q want %q [beads-1nvr5]", got.Description, wantBody)
		}
		if got.Design != wantBody {
			t.Errorf("design: got %q want %q [beads-1nvr5]", got.Design, wantBody)
		}
		if got.Notes != wantBody {
			t.Errorf("notes: got %q want %q [beads-1nvr5]", got.Notes, wantBody)
		}
		if got.AcceptanceCriteria != wantBody {
			t.Errorf("acceptance_criteria: got %q want %q [beads-1nvr5]", got.AcceptanceCriteria, wantBody)
		}
	})

	// Adjacency: two standalone references sharing a single delimiter (one
	// space) must BOTH rewrite — the delimiter-consuming regex needs the
	// loop-until-stable pass, not a single ReplaceAllString.
	t.Run("adjacent_references_both_rewritten", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ra")
		bdCreate(t, bd, dir, "target", "--type", "task", "--id", "ra-abc")
		refs := bdCreate(t, bd, dir, "refs", "--type", "task", "--id", "ra-ref",
			"-d", "ra-abc ra-abc ra-abc")

		bdRename(t, bd, dir, "ra-abc", "ra-xyz")

		got := showIssue(t, bd, dir, refs.ID)
		want := "ra-xyz ra-xyz ra-xyz"
		if got.Description != want {
			t.Errorf("adjacent standalone references not all rewritten\n got: %q\nwant: %q [beads-1nvr5]",
				got.Description, want)
		}
	})
}

// TestProxiedRenameIDBoundary_1nvr5 is the proxied twin: the same id-boundary
// discrimination through the proxied-server rename path, which shares the
// idReferenceRewriter helper (identical regex, identical fix).
func TestProxiedRenameIDBoundary_1nvr5(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("sibling_id_preserved_standalone_rewritten_proxied", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pb")
		bdProxiedCreate(t, bd, p.dir, "target", "--type", "task", "--id", "pb-abc")
		refs := bdProxiedCreate(t, bd, p.dir, "refs", "--type", "task", "--id", "pb-ref",
			"-d", "relates to pb-abc-2 and also pb-abc and pb-abcx")

		if _, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "rename", "pb-abc", "pb-xyz"); err != nil {
			t.Fatalf("proxied rename failed: %v\n%s", err, stderr)
		}

		shown := bdProxiedShowRaw(t, bd, p.dir, refs.ID)
		if !strings.Contains(shown, "pb-abc-2") {
			t.Errorf("proxied rename CORRUPTED the distinct sibling id pb-abc-2 (must be preserved):\n%s [beads-1nvr5]", shown)
		}
		if !strings.Contains(shown, "pb-xyz") {
			t.Errorf("proxied rename did not rewrite the standalone pb-abc token to pb-xyz:\n%s [beads-1nvr5]", shown)
		}
		if !strings.Contains(shown, "pb-abcx") {
			t.Errorf("proxied rename corrupted the trailing-word-char id pb-abcx (must be preserved):\n%s [beads-1nvr5]", shown)
		}
		// Ensure the corruption form specifically did NOT occur.
		if strings.Contains(shown, "pb-xyz-2") {
			t.Errorf("proxied rename produced the corruption pb-xyz-2 from sibling pb-abc-2:\n%s [beads-1nvr5]", shown)
		}
	})

	// Guard against the shared-helper diverging: a quick sanity that the direct
	// and proxied paths agree on a benign standalone rewrite.
	t.Run("standalone_rewrite_matches_direct", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pm")
		bdProxiedCreate(t, bd, p.dir, "target", "--type", "task", "--id", "pm-abc")
		refs := bdProxiedCreate(t, bd, p.dir, "refs", "--type", "task", "--id", "pm-ref",
			"-d", fmt.Sprintf("see %s for details", "pm-abc"))

		if _, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "rename", "pm-abc", "pm-xyz"); err != nil {
			t.Fatalf("proxied rename failed: %v\n%s", err, stderr)
		}
		shown := bdProxiedShowRaw(t, bd, p.dir, refs.ID)
		if !strings.Contains(shown, "see pm-xyz for details") {
			t.Errorf("proxied standalone rewrite did not produce expected text:\n%s [beads-1nvr5]", shown)
		}
	})
}
