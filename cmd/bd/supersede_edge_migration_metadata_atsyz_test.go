//go:build cgo

package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// beads-atsyz: `bd supersede OLD --with NEW` migrates OLD's incoming structural
// edges to NEW (beads-0c9d1), but the migration loop reconstructed the moved
// Dependency from GetDependentsWithMetadata, which returns only {Issue,
// DependencyType} and DROPS the per-edge Metadata JSON blob. For a waits-for
// edge that blob carries the fanout GATE config (gate=all-children /
// any-children, spawner_id — types.WaitsForMeta). So migrating a
// non-default-gate waits-for edge silently reverted it to the default
// all-children gate on NEW — a coordination-semantics regression.
//
// The fix mirrors the landed auto-merge path (beads-706mw, duplicates.go):
// before RemoveDependency, re-read the dependent's outbound records via
// tx.GetDependencyRecords and recover the edge's Metadata, then carry it onto
// the reattached Dependency.
//
// End-to-end harness: real bd binary + embedded Dolt. We seed a waits-for edge
// with a NON-default (any-children) gate via `bd create --waits-for OLD
// --waits-for-gate any-children`, supersede OLD with NEW, then read the raw
// dependencies.metadata column to assert the migrated edge on NEW retained the
// any-children gate.
//
// MUTATION-VERIFY: drop the GetDependencyRecords recovery (leave
// migrated.Metadata empty) and the migrated X->NEW edge comes back with an
// empty/default metadata blob → ParseWaitsForGateMetadata returns all-children
// → this test goes RED.
func TestSupersede_PreservesWaitsForGateMetadata_atsyz(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "sam")

	old := bdCreate(t, bd, dir, "old spawner", "--type", "task")
	replacement := bdCreate(t, bd, dir, "new spawner", "--type", "task")

	// dependent waits-for OLD with the NON-default any-children gate. The edge
	// is dependent --waits-for--> old, carrying {"gate":"any-children"} in its
	// dependencies.metadata blob.
	dependent := bdCreate(t, bd, dir, "fanout waiter", "--type", "task",
		"--waits-for", old.ID, "--waits-for-gate", types.WaitsForAnyChildren)

	// Precondition: the seeded edge must actually carry the any-children gate,
	// else the test proves nothing about migration.
	if got := waitsForGateOf(t, beadsDir, "sam", dependent.ID, old.ID); got != types.WaitsForAnyChildren {
		t.Fatalf("precondition: seeded waits-for edge %s->%s has gate %q, want %q",
			dependent.ID, old.ID, got, types.WaitsForAnyChildren)
	}

	cmd := exec.Command(bd, "supersede", old.ID, "--with", replacement.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd supersede failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// beads-atsyz: the waits-for edge migrated to NEW must RETAIN the
	// any-children gate. Without the metadata recovery it comes back empty →
	// ParseWaitsForGateMetadata defaults to all-children.
	if got := waitsForGateOf(t, beadsDir, "sam", dependent.ID, replacement.ID); got != types.WaitsForAnyChildren {
		t.Errorf("migrated waits-for edge %s->%s has gate %q after supersede, want %q — the edge Metadata (gate config) was dropped during migration (beads-atsyz)",
			dependent.ID, replacement.ID, got, types.WaitsForAnyChildren)
	}
	// And the stale edge to the closed source must be gone.
	if depRowCount(t, beadsDir, "sam", dependent.ID, old.ID) != 0 {
		t.Errorf("stale waits-for edge %s->%s still present after supersede (beads-atsyz)", dependent.ID, old.ID)
	}
}

// waitsForGateOf reads the raw dependencies.metadata JSON for the edge
// issueID -> dependsOnID and returns its parsed waits-for gate
// (all-children/any-children). Returns "" if no such edge exists.
func waitsForGateOf(t *testing.T, beadsDir, database, issueID, dependsOnID string) string {
	t.Helper()
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), dataDir, database, "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer cleanup()
	var metadata string
	err = db.QueryRowContext(t.Context(),
		"SELECT COALESCE(metadata, '') FROM dependencies WHERE issue_id = ? AND COALESCE(depends_on_issue_id, depends_on_wisp_id, depends_on_external) = ?",
		issueID, dependsOnID).Scan(&metadata)
	if err != nil {
		// No row (e.g. edge migrated away) → empty gate.
		return ""
	}
	return types.ParseWaitsForGateMetadata(metadata)
}

// depRowCount returns the number of dependency rows from issueID -> dependsOnID.
func depRowCount(t *testing.T, beadsDir, database, issueID, dependsOnID string) int {
	t.Helper()
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), dataDir, database, "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer cleanup()
	var count int
	if err := db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND COALESCE(depends_on_issue_id, depends_on_wisp_id, depends_on_external) = ?",
		issueID, dependsOnID).Scan(&count); err != nil {
		t.Fatalf("query dependencies: %v", err)
	}
	return count
}
