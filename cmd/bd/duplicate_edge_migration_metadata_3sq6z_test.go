//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-3sq6z: the unpatched TWIN of beads-atsyz. `bd duplicate OLD --of CANON`
// migrates OLD's incoming structural edges to CANON (runDuplicate,
// duplicate.go), exactly like runSupersede. beads-atsyz patched only the
// supersede half; the duplicate migration loop still reconstructed the moved
// Dependency from GetDependentsWithMetadata (which returns only {Issue,
// DependencyType} and DROPS the per-edge Metadata JSON), so a migrated
// waits-for edge silently lost its fanout GATE config (gate=any-children /
// spawner_id — types.WaitsForMeta) and reverted to the default all-children
// gate on the canonical.
//
// The fix mirrors the landed supersede/706mw pattern: before RemoveDependency,
// re-read the dependent's outbound records via tx.GetDependencyRecords, recover
// the edge's Metadata, and carry it onto the reattached Dependency.
//
// End-to-end harness mirrors TestSupersede_PreservesWaitsForGateMetadata_atsyz
// (shared helpers waitsForGateOf / depRowCount live in that file). Seed a
// waits-for edge with a NON-default (any-children) gate, `bd duplicate OLD --of
// CANON`, then read the raw dependencies.metadata column to assert the migrated
// edge on CANON retained the any-children gate.
//
// MUTATION-VERIFY: drop the GetDependencyRecords recovery in runDuplicate
// (leave migrated.Metadata empty) → the migrated dependent->CANON edge comes
// back with empty/default metadata → ParseWaitsForGateMetadata returns
// all-children → this test goes RED.
func TestDuplicate_PreservesWaitsForGateMetadata_3sq6z(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "dup")

	old := bdCreate(t, bd, dir, "old spawner", "--type", "task")
	canonical := bdCreate(t, bd, dir, "canonical spawner", "--type", "task")

	// dependent waits-for OLD with the NON-default any-children gate. The edge
	// is dependent --waits-for--> old, carrying {"gate":"any-children"} in its
	// dependencies.metadata blob.
	dependent := bdCreate(t, bd, dir, "fanout waiter", "--type", "task",
		"--waits-for", old.ID, "--waits-for-gate", types.WaitsForAnyChildren)

	// Precondition: the seeded edge must actually carry the any-children gate,
	// else the test proves nothing about migration.
	if got := waitsForGateOf(t, beadsDir, "dup", dependent.ID, old.ID); got != types.WaitsForAnyChildren {
		t.Fatalf("precondition: seeded waits-for edge %s->%s has gate %q, want %q",
			dependent.ID, old.ID, got, types.WaitsForAnyChildren)
	}

	cmd := exec.Command(bd, "duplicate", old.ID, "--of", canonical.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd duplicate failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// beads-3sq6z: the waits-for edge migrated to CANON must RETAIN the
	// any-children gate. Without the metadata recovery it comes back empty →
	// ParseWaitsForGateMetadata defaults to all-children.
	if got := waitsForGateOf(t, beadsDir, "dup", dependent.ID, canonical.ID); got != types.WaitsForAnyChildren {
		t.Errorf("migrated waits-for edge %s->%s has gate %q after duplicate, want %q — the edge Metadata (gate config) was dropped during migration (beads-3sq6z)",
			dependent.ID, canonical.ID, got, types.WaitsForAnyChildren)
	}
	// And the stale edge to the now-duplicate source must be gone.
	if depRowCount(t, beadsDir, "dup", dependent.ID, old.ID) != 0 {
		t.Errorf("stale waits-for edge %s->%s still present after duplicate (beads-3sq6z)", dependent.ID, old.ID)
	}
}
