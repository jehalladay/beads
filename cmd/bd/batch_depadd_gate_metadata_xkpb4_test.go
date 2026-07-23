//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// beads-xkpb4: a same-type dependency re-add REFRESHES the edge's metadata to
// the caller-supplied blob (AddDependencyInTx / dependencySQLRepositoryImpl.
// Insert do `UPDATE metadata = ?`). The single-edge `bd dep add` path is
// shielded by the beads-bwla same-type pre-check ("already present, no
// change"), but `bd batch dep.add` (and bulk `bd dep add --file`) bypass that
// guard and write straight to tx.AddDependency with a Dependency carrying NO
// Metadata — neither grammar can even express --waits-for-gate. Before the fix
// that empty blob was coerced to "{}" and overwrote an existing
// {"gate":"any-children"} waits-for edge, silently reverting it to the
// all-children default (ParseWaitsForGateMetadata returns all-children on
// empty). Same edge-metadata-loss class as beads-atsyz, on the write re-add
// path rather than migration.
//
// The fix: only refresh metadata on a same-type re-add when dep.Metadata != ""
// (an intentional refresh always carries a blob); a no-metadata re-add
// preserves the existing row's gate.
//
// End-to-end through the ACTUAL `bd batch` subprocess (a tx-helper would
// false-green by skipping the CLI/storage seam); the surviving gate is read via
// `bd export`, the same observable used by beads-gnopw. MUTATION-VERIFY: restore
// the unconditional `UPDATE metadata = ?` and the gate collapses to "{}"
// (all-children) → this test goes RED.
func TestBatchDepAdd_PreservesWaitsForGateMetadata_xkpb4(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bgx")

	old := bdCreate(t, bd, dir, "blocker", "--type", "task")

	// dependent --waits-for--> old with the NON-default any-children gate.
	dependent := bdCreate(t, bd, dir, "fanout waiter", "--type", "task",
		"--waits-for", old.ID, "--waits-for-gate", "any-children")

	// Precondition: the seeded edge carries the any-children gate in export.
	if pre := bdExport(t, bd, dir); !strings.Contains(pre, "any-children") {
		t.Fatalf("precondition: seeded waits-for edge %s->%s should export the any-children gate:\n%s",
			dependent.ID, old.ID, pre)
	}

	// Re-add the SAME waits-for edge via `bd batch dep.add`. The batch grammar
	// has no gate syntax, so this carries no metadata; it must be a benign
	// idempotent no-op, NOT a gate wipe.
	script := "dep add " + dependent.ID + " " + old.ID + " waits-for\n"
	cmd := exec.Command(bd, "batch")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	cmd.Stdin = strings.NewReader(script)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd batch dep.add failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// beads-xkpb4: the waits-for edge must STILL carry the any-children gate.
	post := bdExport(t, bd, dir)
	if !strings.Contains(post, "any-children") {
		t.Errorf("waits-for edge %s->%s lost its any-children gate after batch re-add — "+
			"the batch re-add clobbered the edge metadata (beads-xkpb4):\n%s",
			dependent.ID, old.ID, post)
	}
	if strings.Contains(post, `"metadata":"{}"`) || strings.Contains(post, `"metadata": "{}"`) {
		t.Errorf("beads-xkpb4: batch re-add collapsed the waits-for edge metadata to {} "+
			"(any-children gate lost -> reads as all-children):\n%s", post)
	}
}
