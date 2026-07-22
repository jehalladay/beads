//go:build cgo

package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// beads-706mw: `bd duplicates --auto-merge` (performMerge, duplicates.go)
// re-parents children before closing the merge-loser (source), but the reparent
// loop USED to transfer ONLY parent-child edges (`if dep.DependencyType !=
// types.DepParentChild { continue }`). Every inbound BLOCKING edge (blocks /
// conditional-blocks / waits-for) pointing at the source was therefore skipped,
// then evaporated when the source was closed at CloseIssue — so an issue that
// was blocked-BY the loser silently became UNBLOCKED (released into ready work)
// even though the surviving canonical it merged into may still be open.
//
// The fix transfers blocking edges too: a dependent blocked-by the loser must
// end up blocked-by the CANONICAL after the merge.
//
// MUTATION-VERIFIED: restore the parent-child-only filter
// (`if dep.DependencyType != types.DepParentChild { continue }`) and the D->C
// blocks edge is never created (D->L dies with the closed loser) → this test
// goes RED (D->canonical absent; D silently unblocked).
func TestDuplicatesAutoMerge_TransfersBlockingEdgeToCanonical_706mw(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "d76")

	// Canonical survivor C and its title-twin loser L. findDuplicateGroups keys
	// on ALL content fields, so both get only a title (empty desc/design/AC).
	canonical := bdCreate(t, bd, dir, "shared duplicate title", "--type", "task")
	loser := bdCreate(t, bd, dir, "shared duplicate title", "--type", "task")

	// The edge under test: dependent D is blocked-BY the loser.
	// `dep add D L --type blocks` stores issue_id=D, depends_on=L, type=blocks
	// (D depends on / is blocked by L). This gives L a structural weight of 3
	// (one blocks-dependent), which alone would make L WIN as the merge target.
	dependent := bdCreate(t, bd, dir, "blocked dependent", "--type", "task")
	bdDep(t, bd, dir, "add", dependent.ID, loser.ID, "--type", "blocks")

	// Balance the structural weight so the CANONICAL becomes the target and the
	// loser becomes the SOURCE that gets merged away+closed: give C its own
	// blocks-dependent (weight 3, now tied with L), then break the tie with a
	// text-referrer that mentions C's ID (chooseMergeTarget: equal weight ->
	// higher text-reference count wins). C -> reference count 1, L -> 0.
	guard := bdCreate(t, bd, dir, "canonical guard dependent", "--type", "task")
	bdDep(t, bd, dir, "add", guard.ID, canonical.ID, "--type", "blocks")
	bdCreate(t, bd, dir, "referrer", "--type", "task",
		"--description", "see "+canonical.ID+" for context")

	cmd := exec.Command(bd, "duplicates", "--auto-merge")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd duplicates --auto-merge` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// Precondition: the LOSER must be the source that got closed (else the setup
	// picked the wrong merge target and the test proves nothing).
	if l := bdShow(t, bd, dir, loser.ID); l.Status != types.StatusClosed {
		t.Fatalf("precondition: loser %s was not the merge source (status=%q); test setup did not close it\nstdout:\n%s",
			loser.ID, l.Status, stdout.String())
	}
	if c := bdShow(t, bd, dir, canonical.ID); c.Status == types.StatusClosed {
		t.Fatalf("precondition: canonical %s was closed — it should be the surviving target\nstdout:\n%s",
			canonical.ID, stdout.String())
	}

	// THE FIX: D's blocking edge must now point at the CANONICAL (blocked-by C),
	// carrying the same 'blocks' type — the block transferred to the survivor.
	assertDepExistsWithType(t, beadsDir, "d76", dependent.ID, canonical.ID, "blocks")

	// And the stale edge to the closed loser must be gone (not both).
	assertDepAbsent(t, beadsDir, "d76", dependent.ID, loser.ID)
}

// assertDepAbsent verifies NO dependency row exists from issueID -> dependsOnID.
func assertDepAbsent(t *testing.T, beadsDir, database, issueID, dependsOnID string) {
	t.Helper()
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), dataDir, database, "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer cleanup()
	var count int
	err = db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND COALESCE(depends_on_issue_id, depends_on_wisp_id, depends_on_external) = ?",
		issueID, dependsOnID).Scan(&count)
	if err != nil {
		t.Fatalf("query dependencies: %v", err)
	}
	if count != 0 {
		t.Errorf("stale dependency %s -> %s still present after merge (want it transferred to the canonical), count=%d",
			issueID, dependsOnID, count)
	}
}
