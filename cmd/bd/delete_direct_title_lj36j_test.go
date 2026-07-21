//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// beads-lj36j (direct-path completion of beads-989m0): 989m0 added the title
// match/rewrite to the DOMAIN rewriter (issue_delete.go rewriteTextReferences),
// which the PROXIED delete leg routes through — but the DIRECT/embedded delete
// path (the default for embedded bd) has its OWN inline rewrite at two sites
// (cmd/bd/delete.go single-force block + updateTextReferencesInIssues for
// batch/cascade) that both rewrote description/notes/design/acceptance_criteria
// and SKIPPED title. So a connected neighbor that referenced a deleted id in its
// TITLE kept a dangling live ref in the exact field shown in every
// list/ready/blocked/show view — the same within-bead inconsistency 989m0
// targeted, still live on the direct path.
//
// End-to-end through the ACTUAL `bd delete` subprocess (embedded/direct — NOT
// proxied). MUTATION-VERIFIED: reverting the two direct-path title blocks leaves
// the neighbor title unrewritten (live ref) after delete.
func TestEmbeddedDeleteRewritesTitle_lj36j(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// SINGLE `bd delete <target> --force`: a related neighbor referencing the
	// target in BOTH title and description has both rewritten to [deleted:X].
	t.Run("single_delete_rewrites_neighbor_title", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lt")
		target := bdCreate(t, bd, dir, "lt target", "--type", "task")
		neighbor := bdCreate(t, bd, dir, "lt neighbor", "--type", "task")
		bdUpdate(t, bd, dir, neighbor.ID, "--title", "Fix regression from "+target.ID)
		bdUpdate(t, bd, dir, neighbor.ID, "--description", "Root cause traced to "+target.ID+" here.")
		bdDepAdd(t, bd, dir, target.ID, neighbor.ID, "--type", "related")

		bdDelete(t, bd, dir, target.ID, "--force")

		got := bdShow(t, bd, dir, neighbor.ID)
		wantTombstone := "[deleted:" + target.ID + "]"
		if !strings.Contains(got.Title, wantTombstone) {
			t.Errorf("neighbor TITLE should be tombstoned to %q, got %q (lj36j direct single-delete gap)", wantTombstone, got.Title)
		}
		if strings.Contains(got.Title, target.ID) && !strings.Contains(got.Title, wantTombstone) {
			t.Errorf("neighbor TITLE still holds a LIVE ref to the deleted %s: %q", target.ID, got.Title)
		}
		if !strings.Contains(got.Description, wantTombstone) {
			t.Errorf("neighbor DESCRIPTION should also be tombstoned (regression guard), got %q", got.Description)
		}
	})

	// MULTI `bd delete <t1> <t2> --force`: a neighbor whose title references BOTH
	// deleted ids has both tombstoned (multi-deleted-ID mirror correctness — the
	// batch leg's updateTextReferencesInIssues in-memory Title mirror).
	t.Run("multi_delete_rewrites_title_for_both_ids", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lm")
		t1 := bdCreate(t, bd, dir, "lm t1", "--type", "task")
		t2 := bdCreate(t, bd, dir, "lm t2", "--type", "task")
		neighbor := bdCreate(t, bd, dir, "lm neighbor", "--type", "task")
		bdUpdate(t, bd, dir, neighbor.ID, "--title", "Fix "+t1.ID+" and later "+t2.ID)
		bdUpdate(t, bd, dir, neighbor.ID, "--description", "desc mentions "+t1.ID+" only")
		bdDepAdd(t, bd, dir, t1.ID, neighbor.ID, "--type", "related")
		bdDepAdd(t, bd, dir, t2.ID, neighbor.ID, "--type", "related")

		bdDelete(t, bd, dir, t1.ID, t2.ID, "--force")

		got := bdShow(t, bd, dir, neighbor.ID)
		for _, id := range []string{t1.ID, t2.ID} {
			want := "[deleted:" + id + "]"
			if !strings.Contains(got.Title, want) {
				t.Errorf("neighbor TITLE should tombstone %s → %q, got %q (lj36j multi-delete mirror)", id, want, got.Title)
			}
		}
	})

	// Scoping control (beads-rir3): a bead referencing the target in its title but
	// NOT dependency-connected must be left untouched (the rewrite only touches
	// connected neighbors). Proves the title fix inherits the dep-connected
	// scoping rather than a global title scan.
	t.Run("unconnected_title_ref_left_alone", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lu")
		target := bdCreate(t, bd, dir, "lu target", "--type", "task")
		stranger := bdCreate(t, bd, dir, "lu stranger", "--type", "task")
		liveTitle := "mentions " + target.ID + " but not connected"
		bdUpdate(t, bd, dir, stranger.ID, "--title", liveTitle)
		// no dep edge between target and stranger

		bdDelete(t, bd, dir, target.ID, "--force")

		got := bdShow(t, bd, dir, stranger.ID)
		if got.Title != liveTitle {
			t.Errorf("unconnected bead TITLE must be untouched (rir3 scoping), want %q got %q", liveTitle, got.Title)
		}
	})
}
