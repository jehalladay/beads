//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestDepAddSupersedesCycle_8ix02 covers beads-8ix02: `bd dep add --type
// supersedes` went through AddDependencyInTx → CheckDependencyCycleInTx, but
// cycleCheckTypesFor(DepSupersedes) used to return nil (walked NO edge family),
// so a mutual supersede cycle — `dep add --type supersedes A B` then `B A` —
// was accepted, corrupting the supersede graph (a tracer loops forever). The
// supersede-seam reciprocal guard (beads-02v2k, cmd/bd/duplicate.go) does not
// cover the dep-add path. cycleCheckTypesFor now walks the supersedes graph, so
// the shared seam rejects the cycle for ALL entry points while still allowing a
// legitimate forward version chain (v1→v2→v3, which has no cycle).
func TestDepAddSupersedesCycle_8ix02(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dsc")

	t.Run("mutual_cycle_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "supersede cycle A", "--type", "task")
		b := bdCreate(t, bd, dir, "supersede cycle B", "--type", "task")

		// A superseded-by B is fine.
		if out := bdDep(t, bd, dir, "add", a.ID, b.ID, "--type", "supersedes"); !strings.Contains(out, "Added") {
			t.Fatalf("dep add --type supersedes A B should succeed, got:\n%s", out)
		}
		// B superseded-by A closes a mutual cycle → must be REJECTED.
		out := bdDepFail(t, bd, dir, "add", b.ID, a.ID, "--type", "supersedes")
		if strings.Contains(out, "Added") {
			t.Errorf("dep add --type supersedes B A closed a mutual cycle but was accepted (beads-8ix02):\n%s", out)
		}
		if !strings.Contains(out, "cycle") {
			t.Errorf("expected a cycle rejection, got:\n%s", out)
		}
	})

	t.Run("longer_cycle_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "chain A", "--type", "task")
		b := bdCreate(t, bd, dir, "chain B", "--type", "task")
		c := bdCreate(t, bd, dir, "chain C", "--type", "task")

		bdDep(t, bd, dir, "add", a.ID, b.ID, "--type", "supersedes")
		bdDep(t, bd, dir, "add", b.ID, c.ID, "--type", "supersedes")
		// C→A would close the A→B→C→A loop.
		out := bdDepFail(t, bd, dir, "add", c.ID, a.ID, "--type", "supersedes")
		if strings.Contains(out, "Added") {
			t.Errorf("dep add --type supersedes C A closed a 3-node cycle but was accepted (beads-8ix02):\n%s", out)
		}
		if !strings.Contains(out, "cycle") {
			t.Errorf("expected a cycle rejection for the 3-node loop, got:\n%s", out)
		}
	})

	t.Run("legal_version_chain_allowed", func(t *testing.T) {
		// A forward version chain v1→v2→v3 is legitimate and MUST stay allowed
		// (no cycle) — this is the deliberate behavior the old nil-exclusion
		// protected, now preserved by the reachability walk.
		v1 := bdCreate(t, bd, dir, "version 1", "--type", "task")
		v2 := bdCreate(t, bd, dir, "version 2", "--type", "task")
		v3 := bdCreate(t, bd, dir, "version 3", "--type", "task")

		if out := bdDep(t, bd, dir, "add", v1.ID, v2.ID, "--type", "supersedes"); !strings.Contains(out, "Added") {
			t.Errorf("legal chain v1→v2 should be allowed, got:\n%s", out)
		}
		if out := bdDep(t, bd, dir, "add", v2.ID, v3.ID, "--type", "supersedes"); !strings.Contains(out, "Added") {
			t.Errorf("legal chain v2→v3 should be allowed, got:\n%s", out)
		}
	})
}
