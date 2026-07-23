//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-q8hxe: the shared proxied helper runLinkAndCloseProxied serves BOTH
// `bd supersede OLD --with NEW` and `bd duplicate DUP --of CANON`, and both close
// the source (fromID). On the DIRECT supersede path, beads-0c9d1 migrates the
// source's INCOMING structural edges (blocks / conditional-blocks / waits-for /
// parent-child) to the target before the close — otherwise every dependent of
// the source is left dangling on the now-CLOSED source while the target (which
// inherits the work) stays OPEN:
//   - an incoming blocks/waits-for edge → the ready/blocked engine treats the
//     closed source as a satisfied blocker → the dependent silently UNBLOCKS
//     even though the real successor is not done (premature-actionable);
//   - an incoming parent-child edge → the child orphans onto the closed source
//     instead of reparenting to the target.
// The PROXIED helper did NOT migrate, so a hub-connected (proxied-server) crew's
// duplicate/supersede was a bypass of the 0c9d1 fix (dfzre: fix the class at
// BOTH direct + proxied). Provenance edges (related/duplicates/supersedes/
// discovered-from) are NOT migrated — they legitimately keep pointing at the
// historical source.
//
// End-to-end through the real `bd` proxied-server subprocess
// (BEADS_TEST_PROXIED_SERVER=1) + real ready/blocked engine, mirroring the
// direct 0c9d1 tests and the proxied 26gea legs.

// inReadyProxiedQ8hxe reports whether id appears in the proxied `bd ready --json`.
func inReadyProxiedQ8hxe(t *testing.T, bd string, p proxiedProject, id string) bool {
	t.Helper()
	for _, i := range bdProxiedReadyJSON(t, bd, p) {
		if i != nil && i.Issue != nil && i.Issue.ID == id {
			return true
		}
	}
	return false
}

// dependsOnProxiedQ8hxe reports whether issue `id` has an outgoing dependency on
// `target` of the given type, read from the proxied `bd show --json`.
func dependsOnProxiedQ8hxe(t *testing.T, bd string, p proxiedProject, id, target string, depType types.DependencyType) bool {
	t.Helper()
	d := bdProxiedShowDetailsFirst(t, bd, p.dir, id, "--json")
	deps, ok := d["dependencies"].([]interface{})
	if !ok {
		return false
	}
	for _, raw := range deps {
		dep, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if dep["id"] == target && dep["dependency_type"] == string(depType) {
			return true
		}
	}
	return false
}

// TestProxiedSupersede_MigratesIncomingBlocksEdge_q8hxe: proxied supersede must
// migrate an incoming blocks edge to the open replacement, so the dependent
// stays blocked (not prematurely READY).
//
// MUTATION-VERIFY: drop the incoming-edge migration loop in
// runLinkAndCloseProxied (or exclude DepBlocks from isMigratableSupersedeEdge)
// and this FAILS — dep's blocker stays on the CLOSED source, the ready/blocked
// engine treats it satisfied, and dep wrongly becomes READY.
func TestProxiedSupersede_MigratesIncomingBlocksEdge_q8hxe(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pqsb")

	old := bdProxiedCreate(t, bd, p.dir, "old spec", "--type", "task")
	replacement := bdProxiedCreate(t, bd, p.dir, "new spec", "--type", "task")
	dep := bdProxiedCreate(t, bd, p.dir, "dependent work", "--type", "task")

	// dep depends-on OLD (blocks). While OLD is open, dep is blocked → NOT ready.
	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", dep.ID, old.ID, "--type", "blocks"); err != nil {
		t.Fatalf("proxied dep add failed: %v\n%s", err, out)
	}
	if inReadyProxiedQ8hxe(t, bd, p, dep.ID) {
		t.Fatalf("baseline broken: %s should be blocked by open %s but is READY", dep.ID, old.ID)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "supersede", old.ID, "--with", replacement.ID); err != nil {
		t.Fatalf("proxied `bd supersede` failed: %v\n%s", err, out)
	}

	if inReadyProxiedQ8hxe(t, bd, p, dep.ID) {
		t.Errorf("%s became READY after superseding its blocker %s — the incoming blocks edge was left dangling on the closed source instead of migrating to the open replacement %s (beads-q8hxe: premature unblock on the proxied path)", dep.ID, old.ID, replacement.ID)
	}
	if !dependsOnProxiedQ8hxe(t, bd, p, dep.ID, replacement.ID, types.DepBlocks) {
		t.Errorf("%s does not depend-on the replacement %s after proxied supersede — blocks edge not migrated (beads-q8hxe)", dep.ID, replacement.ID)
	}
	if dependsOnProxiedQ8hxe(t, bd, p, dep.ID, old.ID, types.DepBlocks) {
		t.Errorf("%s still depends-on the closed source %s after proxied supersede — stale edge not removed (beads-q8hxe)", dep.ID, old.ID)
	}
}

// TestProxiedDuplicate_MigratesIncomingBlocksEdge_q8hxe: the SHARED helper also
// serves `bd duplicate`, which closes the source too — so it must migrate the
// incoming blocks edge to the live canonical exactly as supersede does. This is
// the leg that proves the fix covers BOTH verbs at once.
//
// MUTATION-VERIFY: same as above — dropping the migration loop makes dep
// wrongly READY after the canonical duplicate closes its blocker.
func TestProxiedDuplicate_MigratesIncomingBlocksEdge_q8hxe(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pqdb")

	dupIssue := bdProxiedCreate(t, bd, p.dir, "duplicate spec", "--type", "task")
	canonical := bdProxiedCreate(t, bd, p.dir, "canonical spec", "--type", "task")
	dep := bdProxiedCreate(t, bd, p.dir, "dependent work", "--type", "task")

	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", dep.ID, dupIssue.ID, "--type", "blocks"); err != nil {
		t.Fatalf("proxied dep add failed: %v\n%s", err, out)
	}
	if inReadyProxiedQ8hxe(t, bd, p, dep.ID) {
		t.Fatalf("baseline broken: %s should be blocked by open %s but is READY", dep.ID, dupIssue.ID)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "duplicate", dupIssue.ID, "--of", canonical.ID); err != nil {
		t.Fatalf("proxied `bd duplicate` failed: %v\n%s", err, out)
	}

	if inReadyProxiedQ8hxe(t, bd, p, dep.ID) {
		t.Errorf("%s became READY after marking its blocker %s a duplicate — the incoming blocks edge was left dangling on the closed source instead of migrating to the live canonical %s (beads-q8hxe: premature unblock on the proxied duplicate path)", dep.ID, dupIssue.ID, canonical.ID)
	}
	if !dependsOnProxiedQ8hxe(t, bd, p, dep.ID, canonical.ID, types.DepBlocks) {
		t.Errorf("%s does not depend-on the canonical %s after proxied duplicate — blocks edge not migrated (beads-q8hxe)", dep.ID, canonical.ID)
	}
}

// TestProxiedSupersede_ReparentsIncomingParentChildEdge_q8hxe: an incoming
// parent-child edge must be reparented to the replacement, not orphaned on the
// closed source.
//
// MUTATION-VERIFY: exclude DepParentChild from isMigratableSupersedeEdge and
// this FAILS — the child still parents on the closed source, replacement has no
// children.
func TestProxiedSupersede_ReparentsIncomingParentChildEdge_q8hxe(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pqsp")

	old := bdProxiedCreate(t, bd, p.dir, "old parent", "--type", "epic")
	replacement := bdProxiedCreate(t, bd, p.dir, "new parent", "--type", "epic")
	child := bdProxiedCreate(t, bd, p.dir, "child work", "--type", "task")

	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", child.ID, old.ID, "--type", "parent-child"); err != nil {
		t.Fatalf("proxied dep add failed: %v\n%s", err, out)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "supersede", old.ID, "--with", replacement.ID); err != nil {
		t.Fatalf("proxied `bd supersede` failed: %v\n%s", err, out)
	}

	if !dependsOnProxiedQ8hxe(t, bd, p, child.ID, replacement.ID, types.DepParentChild) {
		t.Errorf("child %s was not reparented to the replacement %s after proxied supersede — parent-child edge orphaned on the closed source (beads-q8hxe)", child.ID, replacement.ID)
	}
	if dependsOnProxiedQ8hxe(t, bd, p, child.ID, old.ID, types.DepParentChild) {
		t.Errorf("child %s still parents on the closed source %s after proxied supersede — stale parent-child edge not removed (beads-q8hxe)", child.ID, old.ID)
	}
}

// TestProxiedSupersede_DoesNotMigrateProvenanceEdge_q8hxe (NEGATIVE): a
// provenance edge (related) must NOT be migrated — it legitimately refers to the
// historical source. Pins the migration to the STRUCTURAL edge set.
//
// MUTATION-VERIFY: make isMigratableSupersedeEdge return true for DepRelated
// (or `return true` unconditionally) and this FAILS — the related edge is
// wrongly re-pointed to the replacement.
func TestProxiedSupersede_DoesNotMigrateProvenanceEdge_q8hxe(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pqsr")

	old := bdProxiedCreate(t, bd, p.dir, "old note target", "--type", "task")
	replacement := bdProxiedCreate(t, bd, p.dir, "replacement", "--type", "task")
	noter := bdProxiedCreate(t, bd, p.dir, "related note", "--type", "task")

	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", noter.ID, old.ID, "--type", "related"); err != nil {
		t.Fatalf("proxied dep add failed: %v\n%s", err, out)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "supersede", old.ID, "--with", replacement.ID); err != nil {
		t.Fatalf("proxied `bd supersede` failed: %v\n%s", err, out)
	}

	if !dependsOnProxiedQ8hxe(t, bd, p, noter.ID, old.ID, types.DepRelated) {
		t.Errorf("related edge %s→%s was migrated/removed after proxied supersede — provenance edges must stay on the historical source (beads-q8hxe)", noter.ID, old.ID)
	}
	if dependsOnProxiedQ8hxe(t, bd, p, noter.ID, replacement.ID, types.DepRelated) {
		t.Errorf("related edge was re-pointed to the replacement %s — provenance edges must NOT migrate (beads-q8hxe)", replacement.ID)
	}
}
