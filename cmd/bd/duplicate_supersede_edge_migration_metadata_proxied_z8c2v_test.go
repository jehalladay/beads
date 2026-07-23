//go:build cgo

package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-z8c2v: the PROXIED twin of the direct edge-migration-metadata fixes
// (beads-3sq6z runDuplicate / beads-atsyz runSupersede) + the auto-merge path
// (beads-706mw / lookupEdgeMetadataProxied). The shared proxied handler
// runLinkAndCloseProxied (cmd/bd/duplicate_proxied_server.go) serves BOTH
// `bd duplicate DUP --of CANON` and `bd supersede OLD --with NEW`; it migrates
// each incoming migratable structural edge (isMigratableSupersedeEdge, incl.
// DepWaitsFor) by remove+re-add. It reconstructed the moved Dependency from
// ListWithIssueMetadata → GetDependentsWithMetadataInTx, which returns only
// {Issue, DependencyType} and DROPS the per-edge Metadata JSON. For a waits-for
// edge that blob carries the fanout GATE config (gate=any-children + spawner_id,
// types.WaitsForMeta), so migrating a non-default-gate waits-for edge silently
// reverted it to the default all-children gate on the target — for any
// hub-connected (proxiedServerMode, store==nil) crew, which is the town
// majority. The direct halves recover it (3sq6z/atsyz); the sibling proxied
// auto-merge already recovers it via lookupEdgeMetadataProxied; this shared
// duplicate+supersede handler was the sole remaining outlier (4th path).
//
// FIX: before RemoveDependency in runLinkAndCloseProxied, recover the edge
// Metadata via lookupEdgeMetadataProxied and carry it onto the reattached
// Dependency (Metadata: edgeMeta), mirroring the adjacent proxied auto-merge.
//
// End-to-end through the real `bd` proxied-server subprocess
// (BEADS_TEST_PROXIED_SERVER=1) — a UOW-level helper would false-green by
// skipping the CLI/UOW plumbing. The migrated edge's gate is read from the raw
// dependencies.metadata column over the live proxied server (openProxiedDB),
// mirroring atsyz's raw read but through the hub connection.
//
// MUTATION-VERIFY: drop `Metadata: edgeMeta` (leave the migrated edge's Metadata
// empty) in runLinkAndCloseProxied and both migrated edges come back with an
// empty blob → ParseWaitsForGateMetadata defaults to all-children → RED.

// proxiedWaitsForGateOf reads the raw dependencies.metadata JSON for the edge
// issueID -> dependsOnID over the live proxied server and returns its parsed
// waits-for gate (all-children/any-children). Returns "" if no such edge exists.
func proxiedWaitsForGateOf(t *testing.T, db *sql.DB, issueID, dependsOnID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var metadata string
	err := db.QueryRowContext(ctx,
		"SELECT COALESCE(metadata, '') FROM dependencies WHERE issue_id = ? AND COALESCE(depends_on_issue_id, depends_on_wisp_id, depends_on_external) = ?",
		issueID, dependsOnID).Scan(&metadata)
	if err != nil {
		// No row (e.g. edge migrated away) → empty gate.
		return ""
	}
	return types.ParseWaitsForGateMetadata(metadata)
}

// TestProxiedSupersede_PreservesWaitsForGateMetadata_z8c2v: proxied supersede
// must carry the any-children gate onto the edge migrated to the replacement.
func TestProxiedSupersede_PreservesWaitsForGateMetadata_z8c2v(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pzsg")

	old := bdProxiedCreate(t, bd, p.dir, "old spawner", "--type", "task")
	replacement := bdProxiedCreate(t, bd, p.dir, "new spawner", "--type", "task")

	// dependent waits-for OLD with the NON-default any-children gate.
	dependent := bdProxiedCreate(t, bd, p.dir, "fanout waiter", "--type", "task",
		"--waits-for", old.ID, "--waits-for-gate", string(types.WaitsForAnyChildren))

	db := openProxiedDB(t, p)

	// Precondition: the seeded edge must actually carry the any-children gate.
	if got := proxiedWaitsForGateOf(t, db, dependent.ID, old.ID); got != string(types.WaitsForAnyChildren) {
		t.Fatalf("precondition: seeded waits-for edge %s->%s has gate %q, want %q",
			dependent.ID, old.ID, got, types.WaitsForAnyChildren)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "supersede", old.ID, "--with", replacement.ID); err != nil {
		t.Fatalf("proxied `bd supersede` failed: %v\n%s", err, out)
	}

	if got := proxiedWaitsForGateOf(t, db, dependent.ID, replacement.ID); got != string(types.WaitsForAnyChildren) {
		t.Errorf("migrated waits-for edge %s->%s has gate %q after proxied supersede, want %q — the edge Metadata (gate config) was dropped during migration (beads-z8c2v)",
			dependent.ID, replacement.ID, got, types.WaitsForAnyChildren)
	}
}

// TestProxiedDuplicate_PreservesWaitsForGateMetadata_z8c2v: the SHARED handler
// also serves `bd duplicate`, which closes the source too — so it must carry the
// any-children gate onto the edge migrated to the live canonical exactly as
// supersede does. This is the leg proving the fix covers BOTH verbs at once.
func TestProxiedDuplicate_PreservesWaitsForGateMetadata_z8c2v(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pzdg")

	dupIssue := bdProxiedCreate(t, bd, p.dir, "duplicate spawner", "--type", "task")
	canonical := bdProxiedCreate(t, bd, p.dir, "canonical spawner", "--type", "task")

	dependent := bdProxiedCreate(t, bd, p.dir, "fanout waiter", "--type", "task",
		"--waits-for", dupIssue.ID, "--waits-for-gate", string(types.WaitsForAnyChildren))

	db := openProxiedDB(t, p)

	if got := proxiedWaitsForGateOf(t, db, dependent.ID, dupIssue.ID); got != string(types.WaitsForAnyChildren) {
		t.Fatalf("precondition: seeded waits-for edge %s->%s has gate %q, want %q",
			dependent.ID, dupIssue.ID, got, types.WaitsForAnyChildren)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "duplicate", dupIssue.ID, "--of", canonical.ID); err != nil {
		t.Fatalf("proxied `bd duplicate` failed: %v\n%s", err, out)
	}

	if got := proxiedWaitsForGateOf(t, db, dependent.ID, canonical.ID); got != string(types.WaitsForAnyChildren) {
		t.Errorf("migrated waits-for edge %s->%s has gate %q after proxied duplicate, want %q — the edge Metadata (gate config) was dropped during migration (beads-z8c2v)",
			dependent.ID, canonical.ID, got, types.WaitsForAnyChildren)
	}
}
