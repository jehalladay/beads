//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-z252q: `bd duplicates --auto-merge` (performMerge, duplicates.go) closes
// each merged-away source via the transaction seam but — like `bd update
// --status closed` (beads-zzp26), `bd duplicate`/supersede (beads-26gea), and
// `bd batch` (beads-8cxe6) — bypassed the cmd-layer completed-molecule auto-close
// cascade `bd close` runs (close.go:223). beads-26gea fixed duplicate.go but NOT
// this duplicates.go / performMerge leg (grep -c autoClose duplicates.go == 0).
// So auto-merging a molecule/wisp/template-epic's FINAL open step left the
// auto-closing root stuck OPEN (orphaned-completed-root).
//
// MUTATION-VERIFIED: removing the autoCloseCompletedMolecule call in
// performMerge leaves the root OPEN → this test goes RED.
func TestDuplicatesAutoMerge_AutoClosesCompletedMolecule_z252q(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dqz")

	// A molecule whose only remaining OPEN step is `lastStep`.
	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)

	// Give lastStep a title-duplicate TWIN, and make the TWIN win as the merge
	// target (so lastStep is the SOURCE that gets closed). With equal structural
	// weight, chooseMergeTarget breaks the tie by text-reference count: seed a
	// referrer issue whose description mentions the twin's ID so the twin scores
	// one ref and lastStep scores none → the twin becomes the target and the
	// auto-merge closes lastStep (the molecule's final step) → root must
	// auto-close.
	// NB: findDuplicateGroups keys on ALL content fields (title/description/
	// design/AC/status) — the twin must match the seeded step's EMPTY
	// description/design/AC, so create it with only a title.
	rename(t, bd, dir, lastStep, "shared duplicate title")
	twin := bdCreate(t, bd, dir, "shared duplicate title", "--type", "task")
	bdCreate(t, bd, dir, "referrer", "--type", "task",
		"--description", "see "+twin.ID+" for context")

	cmd := exec.Command(bd, "duplicates", "--auto-merge")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd duplicates --auto-merge` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// Sanity: lastStep must have been the source that got closed.
	if step := bdShow(t, bd, dir, lastStep); step.Status != types.StatusClosed {
		t.Fatalf("precondition: final step %s was not the merge source (status=%q); test setup did not close it\nstdout:\n%s",
			lastStep, step.Status, stdout.String())
	}

	if root := bdShow(t, bd, dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd duplicates --auto-merge` of the final step did not auto-close the completed molecule (beads-z252q)\nauto-merge stdout:\n%s",
			rootID, root.Status, types.StatusClosed, stdout.String())
	}
}

// Negative (no false positive): auto-merging a source that is NOT a molecule's
// final open step must NOT auto-close the root — the molecule is incomplete.
func TestDuplicatesAutoMerge_NonFinalStepDoesNotAutoCloseRoot_z252q(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dqn")

	// Molecule root with TWO open steps.
	root := bdCreate(t, bd, dir, "molecule root", "--type", "molecule")
	step1 := bdCreate(t, bd, dir, "step 1", "--type", "task")
	step2 := bdCreate(t, bd, dir, "step 2", "--type", "task")
	for _, stepID := range []string{step1.ID, step2.ID} {
		bdDep(t, bd, dir, "add", stepID, root.ID, "--type", "parent-child")
	}

	// Make step1 a duplicate source (twin wins as target via a text reference).
	rename(t, bd, dir, step1.ID, "shared duplicate title")
	twin := bdCreate(t, bd, dir, "shared duplicate title", "--type", "task")
	bdCreate(t, bd, dir, "referrer", "--type", "task",
		"--description", "see "+twin.ID+" for context")

	cmd := exec.Command(bd, "duplicates", "--auto-merge")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd duplicates --auto-merge` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if step := bdShow(t, bd, dir, step1.ID); step.Status != types.StatusClosed {
		t.Fatalf("precondition: step1 %s was not the merge source (status=%q)\nstdout:\n%s", step1.ID, step.Status, stdout.String())
	}
	if root := bdShow(t, bd, dir, root.ID); root.Status == types.StatusClosed {
		t.Errorf("molecule root %s auto-closed after auto-merging only ONE of two steps — the cascade must fire only on real completion (beads-z252q)\nauto-merge stdout:\n%s",
			root.ID, stdout.String())
	}
}

// rename sets an issue's title via `bd update --title`.
func rename(t *testing.T, bd, dir, id, title string) {
	t.Helper()
	cmd := exec.Command(bd, "update", id, "--title", title)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rename %s -> %q failed: %v\n%s", id, title, err, out)
	}
}
