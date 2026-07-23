//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-ox2id: `bd duplicates --auto-merge` under proxied-server mode
// (hub-connected crew) previously fail-loud REJECTED, because performMerge
// (duplicates.go) relied on store.GetDependentsWithMetadata + transact(), which
// have no UOW equivalent while the global `store` is nil. performMergeProxied
// (duplicates_proxied_server.go) mirrors performMerge over per-source UOWs using
// the UOW incoming-dependents read (ListWithIssueMetadata + DepDirectionIn) that
// beads-q8hxe introduced. This is the dfzre-class parity fix: the write path must
// exist at BOTH direct and proxied, not just be gated off on the hub-connected
// path.
//
// These teeth drive the REAL `bd duplicates --auto-merge` through the proxied
// server subprocess (BEADS_TEST_PROXIED_SERVER=1) end-to-end, mirroring the
// direct performMerge behavior beads-zcq86/706mw/chf1w/z252q established:
//   - the merged-away source closes AND links to the canonical with a
//     types.DepDuplicates edge (not "related");
//   - an incoming blocks edge on the source (loser) migrates to the surviving
//     canonical so the dependent stays blocked (not premature-ready — 706mw);
//   - a parent-child child reparents to the canonical instead of orphaning.
//
// Helpers inReadyProxiedQ8hxe / dependsOnProxiedQ8hxe are shared from the q8hxe
// test file (same package).

// dupGroupContent gives two issues identical content (title/description) so
// findDuplicateGroups groups them; distinct issues in the same project that must
// NOT join the group get different titles.
func mkDupPairProxiedOx2id(t *testing.T, bd string, p proxiedProject, title, desc string) (*types.Issue, *types.Issue) {
	t.Helper()
	a := bdProxiedCreate(t, bd, p.dir, title, "-d", desc, "--type", "task")
	b := bdProxiedCreate(t, bd, p.dir, title, "-d", desc, "--type", "task")
	return a, b
}

// TestProxiedDuplicatesAutoMerge_MigratesIncomingBlocksEdge_ox2id: a dependent
// blocks-depends on the merge LOSER; after --auto-merge the edge must follow the
// surviving canonical so the dependent stays blocked, not prematurely ready.
//
// MUTATION-VERIFY: revert the caller to `return HandleError(... not supported
// ...)` for proxied --auto-merge (or drop the 706mw edge-transfer loop in
// performMergeProxied) → the auto-merge either errors (no merge happens, source
// stays open, edge un-migrated) or the dependent unblocks — either way the
// migrated-edge + not-ready asserts FAIL.
func TestProxiedDuplicatesAutoMerge_MigratesIncomingBlocksEdge_ox2id(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "poxb")

	// Two identical-content issues form a duplicate group. Give ONE of them an
	// extra dependent so it wins chooseMergeTarget (dependents weigh 3x), making
	// the merge target deterministic regardless of ID ordering.
	keep, drop := mkDupPairProxiedOx2id(t, bd, p, "dup spec", "same body")
	// chooseMergeTarget weights dependents 3x — but GetDependencyCounts scores
	// ONLY 'blocks' edges (parent-child edges contribute ZERO weight). The loser
	// `drop` will carry ONE incoming blocks edge under test (dep, below) →
	// DependentCount 1 → weight 3. Give `keep` TWO incoming blocks edges →
	// DependentCount 2 → weight 6 > 3, so keep wins deterministically as the
	// canonical target regardless of ID ordering.
	for i, title := range []string{"keep weight blocker A", "keep weight blocker B"} {
		wc := bdProxiedCreate(t, bd, p.dir, title, "--type", "task")
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", wc.ID, keep.ID, "--type", "blocks"); err != nil {
			t.Fatalf("proxied dep add (weight %d) failed: %v\n%s", i, err, out)
		}
	}

	// dep blocks-depends on `drop` (the loser). While drop is open, dep is blocked.
	dep := bdProxiedCreate(t, bd, p.dir, "dependent work", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", dep.ID, drop.ID, "--type", "blocks"); err != nil {
		t.Fatalf("proxied dep add (blocks) failed: %v\n%s", err, out)
	}
	if inReadyProxiedQ8hxe(t, bd, p, dep.ID) {
		t.Fatalf("baseline broken: %s should be blocked by open %s but is READY", dep.ID, drop.ID)
	}

	// Run the auto-merge against the proxied server. Before beads-ox2id this
	// errored ("not supported against a proxied server").
	if out, err := bdProxiedRun(t, bd, p.dir, "duplicates", "--auto-merge"); err != nil {
		t.Fatalf("proxied `bd duplicates --auto-merge` failed: %v\n%s", err, out)
	}

	// The loser must be closed and linked to the canonical via a duplicates edge.
	if !dependsOnProxiedQ8hxe(t, bd, p, drop.ID, keep.ID, types.DepDuplicates) {
		t.Errorf("%s was not linked as a duplicate of %s after --auto-merge (beads-chf1w/ox2id)", drop.ID, keep.ID)
	}

	// The incoming blocks edge must have migrated drop→keep so dep stays blocked.
	if inReadyProxiedQ8hxe(t, bd, p, dep.ID) {
		t.Errorf("%s became READY after --auto-merge closed its blocker %s — the incoming blocks edge was left dangling on the closed loser instead of migrating to the surviving canonical %s (beads-706mw/ox2id: premature unblock on the proxied auto-merge path)", dep.ID, drop.ID, keep.ID)
	}
	if !dependsOnProxiedQ8hxe(t, bd, p, dep.ID, keep.ID, types.DepBlocks) {
		t.Errorf("%s does not depend-on the canonical %s after --auto-merge — blocks edge not migrated (beads-ox2id)", dep.ID, keep.ID)
	}
	if dependsOnProxiedQ8hxe(t, bd, p, dep.ID, drop.ID, types.DepBlocks) {
		t.Errorf("%s still depends-on the closed loser %s after --auto-merge — stale edge not removed (beads-ox2id)", dep.ID, drop.ID)
	}
}

// TestProxiedDuplicatesAutoMerge_ReparentsIncomingParentChild_ox2id: a child
// parented on the merge loser must reparent to the surviving canonical, not
// orphan on the closed loser.
//
// MUTATION-VERIFY: exclude DepParentChild from the transferred set in
// performMergeProxied (mirror the direct 706mw membership) → the child stays on
// the closed loser and the reparent assert FAILS.
func TestProxiedDuplicatesAutoMerge_ReparentsIncomingParentChild_ox2id(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "poxp")

	keep, drop := mkDupPairProxiedOx2id(t, bd, p, "dup epic body", "identical epic body")
	// The loser `drop` carries ONE child under test (parent-child edge) — but
	// GetDependencyCounts scores ONLY 'blocks' edges, so a parent-child edge gives
	// drop weight 0. Give `keep` TWO incoming blocks edges (weight 6 > 0) so it
	// wins chooseMergeTarget deterministically regardless of ID ordering.
	for i, title := range []string{"keep weight blocker A", "keep weight blocker B"} {
		wc := bdProxiedCreate(t, bd, p.dir, title, "--type", "task")
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", wc.ID, keep.ID, "--type", "blocks"); err != nil {
			t.Fatalf("proxied dep add (weight %d) failed: %v\n%s", i, err, out)
		}
	}

	// child parents on `drop` (the loser).
	child := bdProxiedCreate(t, bd, p.dir, "orphan candidate", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", child.ID, drop.ID, "--type", "parent-child"); err != nil {
		t.Fatalf("proxied dep add (child) failed: %v\n%s", err, out)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "duplicates", "--auto-merge"); err != nil {
		t.Fatalf("proxied `bd duplicates --auto-merge` failed: %v\n%s", err, out)
	}

	if !dependsOnProxiedQ8hxe(t, bd, p, child.ID, keep.ID, types.DepParentChild) {
		t.Errorf("child %s was not reparented to the canonical %s after --auto-merge — parent-child edge orphaned on the closed loser (beads-ox2id)", child.ID, keep.ID)
	}
	if dependsOnProxiedQ8hxe(t, bd, p, child.ID, drop.ID, types.DepParentChild) {
		t.Errorf("child %s still parents on the closed loser %s after --auto-merge — stale parent-child edge not removed (beads-ox2id)", child.ID, drop.ID)
	}
}

// TestProxiedDuplicatesAutoMerge_LeavesProvenanceEdge_ox2id: a knowledge/
// provenance incoming edge (types.DepRelated) legitimately points at the
// historical source and must NOT migrate to the canonical — the transfer gate
// (dep.DependencyType != DepParentChild && !IsBlockingEdge → continue) excludes
// it, mirroring the direct performMerge. This locks the migratable-set boundary:
// an over-broad gate that moved `related` too would fail this negative.
func TestProxiedDuplicatesAutoMerge_LeavesProvenanceEdge_ox2id(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "poxr")

	keep, drop := mkDupPairProxiedOx2id(t, bd, p, "dup prov body", "identical prov body")
	// keep wins via two incoming blocks edges (weight 6 > drop's 0).
	for i, title := range []string{"keep weight blocker A", "keep weight blocker B"} {
		wc := bdProxiedCreate(t, bd, p.dir, title, "--type", "task")
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", wc.ID, keep.ID, "--type", "blocks"); err != nil {
			t.Fatalf("proxied dep add (weight %d) failed: %v\n%s", i, err, out)
		}
	}

	// note relates-to `drop` (a provenance edge, incoming on the loser).
	note := bdProxiedCreate(t, bd, p.dir, "related note", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", note.ID, drop.ID, "--type", "related"); err != nil {
		t.Fatalf("proxied dep add (related) failed: %v\n%s", err, out)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "duplicates", "--auto-merge"); err != nil {
		t.Fatalf("proxied `bd duplicates --auto-merge` failed: %v\n%s", err, out)
	}

	// The merge must have happened (drop closed as a duplicate of keep)...
	if !dependsOnProxiedQ8hxe(t, bd, p, drop.ID, keep.ID, types.DepDuplicates) {
		t.Fatalf("%s was not merged into %s — fixture/target selection broken (beads-ox2id)", drop.ID, keep.ID)
	}
	// ...but the provenance edge must stay on the historical source, NOT migrate.
	if dependsOnProxiedQ8hxe(t, bd, p, note.ID, keep.ID, types.DepRelated) {
		t.Errorf("provenance edge %s→related→%s migrated to the canonical after --auto-merge — the transfer gate is over-broad (beads-706mw/ox2id: only blocks/conditional-blocks/waits-for/parent-child migrate)", note.ID, keep.ID)
	}
	if !dependsOnProxiedQ8hxe(t, bd, p, note.ID, drop.ID, types.DepRelated) {
		t.Errorf("provenance edge %s→related→%s was removed from the historical source after --auto-merge — related edges must be left in place (beads-ox2id)", note.ID, drop.ID)
	}
}
