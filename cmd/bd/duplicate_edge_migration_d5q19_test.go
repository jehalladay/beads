//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-d5q19: `bd duplicate OLD --of CANONICAL` must migrate OLD's incoming
// STRUCTURAL edges (blocks / conditional-blocks / waits-for / parent-child) to
// CANONICAL before closing OLD — at parity with `bd supersede` (beads-0c9d1)
// and `bd duplicates --auto-merge` (beads-706mw, which transfers blocking edges
// to the canonical). CANONICAL is the live successor of the duplicate, exactly
// as the replacement is of a superseded source, so otherwise:
//   - an incoming blocks/waits-for edge → the ready/blocked engine treats the
//     closed duplicate as satisfied → the dependent silently UNBLOCKS even
//     though the live canonical is not done (premature-actionable);
//   - an incoming parent-child edge → the child orphans onto the closed
//     duplicate and never reparents to the canonical.
// Provenance edges (related/duplicates/discovered-from/…) are NOT migrated —
// they legitimately keep pointing at the historical source.
//
// End-to-end harness (real bd binary + embedded Dolt + real ready/blocked
// engine, no fakes), mirroring the supersede 0c9d1 tests. Reuses the
// inReady0c9d1 / dependsOn0c9d1 helpers (same package).

func TestDuplicate_MigratesIncomingBlocksEdge_d5q19(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dmb")

	old := bdCreate(t, bd, dir, "old report", "--type", "task")
	canonical := bdCreate(t, bd, dir, "canonical report", "--type", "task")
	dep := bdCreate(t, bd, dir, "dependent work", "--type", "task")

	// dep depends-on OLD (blocks). While OLD is open, dep is blocked → NOT ready.
	bdDep(t, bd, dir, "add", dep.ID, old.ID, "--type", "blocks")
	if inReady0c9d1(t, bd, dir, dep.ID) {
		t.Fatalf("baseline broken: %s should be blocked by open %s but is READY", dep.ID, old.ID)
	}

	// Mark OLD a duplicate of the still-OPEN canonical.
	cmd := exec.Command(bd, "duplicate", old.ID, "--of", canonical.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd duplicate failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// beads-d5q19: the blocks edge must have been MIGRATED to the canonical, so
	// dep now depends on the OPEN canonical → still blocked, NOT ready.
	//
	// MUTATION-VERIFY: drop the incoming-edge migration loop in runDuplicate (or
	// revert to store.LinkAndClose) and this FAILS — dep's blocker stays on the
	// CLOSED old, the ready/blocked engine treats it satisfied, and dep wrongly
	// becomes READY while the live canonical is open.
	if inReady0c9d1(t, bd, dir, dep.ID) {
		t.Errorf("%s became READY after marking its blocker %s a duplicate — the incoming blocks edge was left dangling on the closed source instead of migrating to the open canonical %s (beads-d5q19: premature unblock)", dep.ID, old.ID, canonical.ID)
	}
	if !dependsOn0c9d1(t, bd, dir, dep.ID, canonical.ID, types.DepBlocks) {
		t.Errorf("%s does not depend-on the canonical %s after duplicate — blocks edge not migrated (beads-d5q19)", dep.ID, canonical.ID)
	}
	if dependsOn0c9d1(t, bd, dir, dep.ID, old.ID, types.DepBlocks) {
		t.Errorf("%s still depends-on the closed source %s after duplicate — stale edge not removed (beads-d5q19)", dep.ID, old.ID)
	}
}

func TestDuplicate_ReparentsIncomingParentChildEdge_d5q19(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dmp")

	old := bdCreate(t, bd, dir, "old parent", "--type", "epic")
	canonical := bdCreate(t, bd, dir, "canonical parent", "--type", "epic")
	child := bdCreate(t, bd, dir, "child work", "--type", "task")

	// child is a parent-child child of OLD.
	bdDep(t, bd, dir, "add", child.ID, old.ID, "--type", "parent-child")

	cmd := exec.Command(bd, "duplicate", old.ID, "--of", canonical.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd duplicate failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// beads-d5q19: the child's parent-child edge must be reparented to the
	// canonical, not orphaned on the closed OLD.
	//
	// MUTATION-VERIFY: drop the migration loop and this FAILS — the child still
	// parents on the closed old, canonical has no children, structure is lost.
	if !dependsOn0c9d1(t, bd, dir, child.ID, canonical.ID, types.DepParentChild) {
		t.Errorf("child %s was not reparented to the canonical %s after duplicate — parent-child edge orphaned on the closed source (beads-d5q19)", child.ID, canonical.ID)
	}
	if dependsOn0c9d1(t, bd, dir, child.ID, old.ID, types.DepParentChild) {
		t.Errorf("child %s still parents on the closed source %s after duplicate — stale parent-child edge not removed (beads-d5q19)", child.ID, old.ID)
	}
}

// NEGATIVE: a provenance edge (related) must NOT be migrated — it legitimately
// keeps referring to the historical source. Pins the migration to the
// STRUCTURAL edge set and guards against an over-broad "migrate everything".
//
// MUTATION-VERIFY: make the loop migrate DepRelated (or drop the
// isMigratableSupersedeEdge filter) and this FAILS — the related edge is
// wrongly re-pointed to the canonical.
func TestDuplicate_DoesNotMigrateProvenanceEdge_d5q19(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dmr")

	old := bdCreate(t, bd, dir, "old note target", "--type", "task")
	canonical := bdCreate(t, bd, dir, "canonical", "--type", "task")
	noter := bdCreate(t, bd, dir, "related note", "--type", "task")

	// noter is "related" to OLD (a provenance/knowledge edge).
	bdDep(t, bd, dir, "add", noter.ID, old.ID, "--type", "related")

	cmd := exec.Command(bd, "duplicate", old.ID, "--of", canonical.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd duplicate failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if !dependsOn0c9d1(t, bd, dir, noter.ID, old.ID, types.DepRelated) {
		t.Errorf("related edge %s→%s was migrated/removed after duplicate — provenance edges must stay on the historical source (beads-d5q19)", noter.ID, old.ID)
	}
	if dependsOn0c9d1(t, bd, dir, noter.ID, canonical.ID, types.DepRelated) {
		t.Errorf("related edge was re-pointed to the canonical %s — provenance edges must NOT migrate (beads-d5q19)", canonical.ID)
	}
}
