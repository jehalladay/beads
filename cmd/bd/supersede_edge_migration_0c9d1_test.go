//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-0c9d1: `bd supersede OLD --with NEW` must migrate OLD's incoming
// STRUCTURAL edges (blocks / conditional-blocks / waits-for / parent-child) to
// NEW before closing OLD — otherwise every dependent of OLD is left dangling on
// the now-CLOSED source while NEW (which inherits OLD's work) stays OPEN:
//   - an incoming blocks/waits-for edge → the ready/blocked engine treats the
//     closed blocker as satisfied → the dependent silently UNBLOCKS even though
//     the real successor NEW is not done (premature-actionable);
//   - an incoming parent-child edge → the child orphans onto the closed OLD and
//     never reparents to NEW.
// Provenance edges (related/duplicates/discovered-from/…) are NOT migrated —
// they legitimately keep pointing at the historical source.
//
// End-to-end harness (real bd binary + embedded Dolt + real ready/blocked
// engine, no fakes), mirroring the r3m8v/26gea supersede tests.

// inReady0c9d1 reports whether id appears in `bd ready --json`.
func inReady0c9d1(t *testing.T, bd, dir, id string) bool {
	t.Helper()
	cmd := exec.Command(bd, "ready", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, _, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd ready --json failed: %v", err)
	}
	var issues []map[string]interface{}
	if jerr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &issues); jerr != nil {
		t.Fatalf("parse ready JSON: %v\n%s", jerr, out.String())
	}
	for _, i := range issues {
		if i["id"] == id {
			return true
		}
	}
	return false
}

// dependsOn0c9d1 reports whether issue `id` has an outgoing dependency on
// `target` of the given type, read from `bd show --json`.
func dependsOn0c9d1(t *testing.T, bd, dir, id, target string, depType types.DependencyType) bool {
	t.Helper()
	cmd := exec.Command(bd, "show", id, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd show %s --json failed: %v\nstderr:\n%s", id, err, stderr.String())
	}
	// `bd show --json` emits a JSON ARRAY (one element per resolved id).
	var list []types.IssueDetails
	if jerr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &list); jerr != nil {
		t.Fatalf("parse show JSON for %s: %v\n%s", id, jerr, out.String())
	}
	if len(list) == 0 {
		t.Fatalf("bd show %s --json returned an empty array", id)
	}
	d := list[0]
	for _, dep := range d.Dependencies {
		if dep.ID == target && dep.DependencyType == depType {
			return true
		}
	}
	return false
}

func TestSupersede_MigratesIncomingBlocksEdge_0c9d1(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "smb")

	old := bdCreate(t, bd, dir, "old spec", "--type", "task")
	replacement := bdCreate(t, bd, dir, "new spec", "--type", "task")
	dep := bdCreate(t, bd, dir, "dependent work", "--type", "task")

	// dep depends-on OLD (blocks). While OLD is open, dep is blocked → NOT ready.
	bdDep(t, bd, dir, "add", dep.ID, old.ID, "--type", "blocks")
	if inReady0c9d1(t, bd, dir, dep.ID) {
		t.Fatalf("baseline broken: %s should be blocked by open %s but is READY", dep.ID, old.ID)
	}

	// Supersede OLD with the still-OPEN replacement.
	cmd := exec.Command(bd, "supersede", old.ID, "--with", replacement.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd supersede failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// beads-0c9d1: the blocks edge must have been MIGRATED to the replacement,
	// so dep now depends on the OPEN replacement → still blocked, NOT ready.
	//
	// MUTATION-VERIFY: drop the incoming-edge migration loop (or exclude
	// DepBlocks from isMigratableSupersedeEdge) and this FAILS — dep's blocker
	// stays on the CLOSED old, the ready/blocked engine treats it satisfied, and
	// dep wrongly becomes READY while the real successor (replacement) is open.
	if inReady0c9d1(t, bd, dir, dep.ID) {
		t.Errorf("%s became READY after superseding its blocker %s — the incoming blocks edge was left dangling on the closed source instead of migrating to the open replacement %s (beads-0c9d1: premature unblock)", dep.ID, old.ID, replacement.ID)
	}
	if !dependsOn0c9d1(t, bd, dir, dep.ID, replacement.ID, types.DepBlocks) {
		t.Errorf("%s does not depend-on the replacement %s after supersede — blocks edge not migrated (beads-0c9d1)", dep.ID, replacement.ID)
	}
	if dependsOn0c9d1(t, bd, dir, dep.ID, old.ID, types.DepBlocks) {
		t.Errorf("%s still depends-on the closed source %s after supersede — stale edge not removed (beads-0c9d1)", dep.ID, old.ID)
	}
}

func TestSupersede_ReparentsIncomingParentChildEdge_0c9d1(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "smp")

	old := bdCreate(t, bd, dir, "old parent", "--type", "epic")
	replacement := bdCreate(t, bd, dir, "new parent", "--type", "epic")
	child := bdCreate(t, bd, dir, "child work", "--type", "task")

	// child is a parent-child child of OLD.
	bdDep(t, bd, dir, "add", child.ID, old.ID, "--type", "parent-child")

	cmd := exec.Command(bd, "supersede", old.ID, "--with", replacement.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd supersede failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// beads-0c9d1: the child's parent-child edge must be reparented to the
	// replacement, not orphaned on the closed OLD.
	//
	// MUTATION-VERIFY: exclude DepParentChild from isMigratableSupersedeEdge and
	// this FAILS — the child still parents on the closed old, replacement has no
	// children, structure is lost.
	if !dependsOn0c9d1(t, bd, dir, child.ID, replacement.ID, types.DepParentChild) {
		t.Errorf("child %s was not reparented to the replacement %s after supersede — parent-child edge orphaned on the closed source (beads-0c9d1)", child.ID, replacement.ID)
	}
	if dependsOn0c9d1(t, bd, dir, child.ID, old.ID, types.DepParentChild) {
		t.Errorf("child %s still parents on the closed source %s after supersede — stale parent-child edge not removed (beads-0c9d1)", child.ID, old.ID)
	}
}

// NEGATIVE: a provenance edge (related) must NOT be migrated — it legitimately
// keeps referring to the historical source. This pins the migration to the
// STRUCTURAL edge set and guards against an over-broad "migrate everything".
//
// MUTATION-VERIFY: make isMigratableSupersedeEdge return true for DepRelated
// (or `return true` unconditionally) and this FAILS — the related edge is
// wrongly re-pointed to the replacement.
func TestSupersede_DoesNotMigrateProvenanceEdge_0c9d1(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "smr")

	old := bdCreate(t, bd, dir, "old note target", "--type", "task")
	replacement := bdCreate(t, bd, dir, "replacement", "--type", "task")
	noter := bdCreate(t, bd, dir, "related note", "--type", "task")

	// noter is "related" to OLD (a provenance/knowledge edge).
	bdDep(t, bd, dir, "add", noter.ID, old.ID, "--type", "related")

	cmd := exec.Command(bd, "supersede", old.ID, "--with", replacement.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd supersede failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if !dependsOn0c9d1(t, bd, dir, noter.ID, old.ID, types.DepRelated) {
		t.Errorf("related edge %s→%s was migrated/removed after supersede — provenance edges must stay on the historical source (beads-0c9d1)", noter.ID, old.ID)
	}
	if dependsOn0c9d1(t, bd, dir, noter.ID, replacement.ID, types.DepRelated) {
		t.Errorf("related edge was re-pointed to the replacement %s — provenance edges must NOT migrate (beads-0c9d1)", replacement.ID)
	}
}
