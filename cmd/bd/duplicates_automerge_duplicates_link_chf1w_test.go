//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-chf1w: `bd duplicates --auto-merge` (performMerge, duplicates.go) links
// each merged-away source to the surviving target so provenance survives. It
// previously minted that edge as types.DependencyType("related"), diverging from
// the canonical mark-duplicate path (`bd duplicate` -> runMarkDuplicate ->
// store.LinkAndClose, duplicate.go), which links source -duplicates-> canonical
// with types.DepDuplicates. Two consequences of the divergence:
//  1. it bypassed the beads-8nugc reopen guard — reopen.go's duplicatesTargets
//     traces ONLY DepDuplicates edges, so a "related"-linked loser was invisible
//     to it and reopening the surviving canonical never reasoned about the
//     merged-away dup;
//  2. it downgraded dup-provenance from "duplicates" to a vague "related".
//
// The fix changes only the edge TYPE (same direction: source/loser -> target/
// canonical) to types.DepDuplicates, matching bd duplicate.
//
// MUTATION-VERIFIED: reverting the type in performMerge to
// types.DependencyType("related") makes this test go RED (the loser->canonical
// edge is "related", not "duplicates").
func TestDuplicatesAutoMerge_LinksSourceAsDuplicates_chf1w(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "dcl")

	// Canonical issue that should WIN the merge as the target.
	canonical := bdCreate(t, bd, dir, "shared duplicate title", "--type", "task")
	// Title-twin loser that should become the SOURCE (closed + linked to canonical).
	loser := bdCreate(t, bd, dir, "shared duplicate title", "--type", "task")

	// Equal structural weight → chooseMergeTarget breaks the tie by text-reference
	// count. Seed a referrer whose description mentions canonical.ID so canonical
	// scores one ref and loser scores none → canonical wins as target and loser is
	// the source that gets closed + duplicate-linked.
	bdCreate(t, bd, dir, "referrer", "--type", "task",
		"--description", "see "+canonical.ID+" for context")

	cmd := exec.Command(bd, "duplicates", "--auto-merge")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd duplicates --auto-merge` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// Precondition: loser must have been the source that got closed.
	if l := bdShow(t, bd, dir, loser.ID); l.Status != types.StatusClosed {
		t.Fatalf("precondition: loser %s was not the merge source (status=%q); test setup did not close it\nstdout:\n%s",
			loser.ID, l.Status, stdout.String())
	}
	// And canonical must still be open (it is the surviving target).
	if c := bdShow(t, bd, dir, canonical.ID); c.Status == types.StatusClosed {
		t.Fatalf("precondition: canonical %s was closed; it should be the surviving target\nstdout:\n%s",
			canonical.ID, stdout.String())
	}

	// The merge-loser must link to the canonical as a "duplicates" edge (beads-chf1w),
	// matching `bd duplicate` — NOT the old "related" edge. This is what makes the
	// merged-away dup visible to the beads-8nugc reopen guard and preserves
	// dup-provenance.
	assertDepExistsWithType(t, beadsDir, "dcl", loser.ID, canonical.ID, string(types.DepDuplicates))
}
