//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// beads-ulq23: the "Suggested" copy-paste hint that `bd duplicates` prints for
// each duplicate group used to be `bd close <src> && bd dep add <src> <target>
// --type related`. That diverged twice from what the automated paths actually
// do: (1) it was the non-atomic pre-close 2-step form, and (2) it minted a
// "related" edge — invisible to the beads-8nugc reopen guard and weaker than
// the types.DepDuplicates edge produced by both `bd duplicate` and (post
// beads-chf1w) `bd duplicates --auto-merge`. Following the hint by hand yielded
// a provenance-divergent result. The fix renders `bd duplicate <src> --of
// <target>` instead (suggestedMergeCommand), aligning the hint with the
// canonical mark-duplicate path.
//
// MUTATION-VERIFIED: reverting suggestedMergeCommand to the old
// "bd close ... && bd dep add ... --type related" text makes this test go RED
// (the hint no longer contains "bd duplicate ... --of ..." and still contains
// the forbidden "dep add ... --type related").
func TestDuplicatesSuggestedHint_UsesBdDuplicate_ulq23(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "shl")

	canonical := bdCreate(t, bd, dir, "shared duplicate title", "--type", "task")
	loser := bdCreate(t, bd, dir, "shared duplicate title", "--type", "task")

	// --dry-run prints the human "Suggested:" hint without merging anything.
	cmd := exec.Command(bd, "duplicates", "--dry-run")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd duplicates --dry-run` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()

	// The hint must suggest the canonical atomic mark-duplicate command. Either
	// source order (canonical/loser) can win the merge target, so accept a
	// `bd duplicate <src> --of <target>` for whichever became the source.
	wantA := "bd duplicate " + loser.ID + " --of " + canonical.ID
	wantB := "bd duplicate " + canonical.ID + " --of " + loser.ID
	if !strings.Contains(out, wantA) && !strings.Contains(out, wantB) {
		t.Fatalf("suggested hint missing canonical `bd duplicate <src> --of <target>` form;\nwant one of:\n  %s\n  %s\ngot:\n%s", wantA, wantB, out)
	}

	// And it must NOT emit the old divergent form.
	if strings.Contains(out, "dep add") || strings.Contains(out, "--type related") {
		t.Fatalf("suggested hint still emits the old `dep add ... --type related` form:\n%s", out)
	}
}
