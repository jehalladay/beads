//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestDepAddWaitsForCycle_vdqra covers beads-vdqra: `bd dep add --type
// waits-for` went through AddDependencyInTx → CheckDependencyCycleInTx, but
// cycleCheckTypesFor(DepWaitsFor) used to fall through to `default: return nil`
// (walked NO edge family), so a mutual waits-for cycle — `dep add --type
// waits-for A B` then `B A` — was accepted, even though the identical sequence
// with --type blocks OR --type conditional-blocks is correctly rejected. A
// waits-for is a HARD BLOCKING edge everywhere else (IsBlockingEdge /
// AffectsReadyWork both include it) and a fanout GATE; a mutual A⇄B (or a longer
// A→B→C→A) is two gates each gating on the other = a latent deadlock that
// corrupts fanout-gate traversal. cycleCheckTypesFor now walks the waits-for
// graph as its own family, so the shared seam rejects the cycle for ALL entry
// points while still allowing a legitimate acyclic fanout-gate chain.
func TestDepAddWaitsForCycle_vdqra(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "wfc")

	t.Run("mutual_cycle_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "waits-for cycle A", "--type", "task")
		b := bdCreate(t, bd, dir, "waits-for cycle B", "--type", "task")

		// A waits-for B is fine.
		if out := bdDep(t, bd, dir, "add", a.ID, b.ID, "--type", "waits-for"); !strings.Contains(out, "Added") {
			t.Fatalf("dep add --type waits-for A B should succeed, got:\n%s", out)
		}
		// B waits-for A closes a mutual cycle → must be REJECTED.
		out := bdDepFail(t, bd, dir, "add", b.ID, a.ID, "--type", "waits-for")
		if strings.Contains(out, "Added") {
			t.Errorf("dep add --type waits-for B A closed a mutual cycle but was accepted (beads-vdqra):\n%s", out)
		}
		if !strings.Contains(out, "cycle") {
			t.Errorf("expected a cycle rejection, got:\n%s", out)
		}
	})

	t.Run("longer_cycle_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "wf chain A", "--type", "task")
		b := bdCreate(t, bd, dir, "wf chain B", "--type", "task")
		c := bdCreate(t, bd, dir, "wf chain C", "--type", "task")

		bdDep(t, bd, dir, "add", a.ID, b.ID, "--type", "waits-for")
		bdDep(t, bd, dir, "add", b.ID, c.ID, "--type", "waits-for")
		// C→A would close the A→B→C→A loop.
		out := bdDepFail(t, bd, dir, "add", c.ID, a.ID, "--type", "waits-for")
		if strings.Contains(out, "Added") {
			t.Errorf("dep add --type waits-for C A closed a 3-node cycle but was accepted (beads-vdqra):\n%s", out)
		}
		if !strings.Contains(out, "cycle") {
			t.Errorf("expected a cycle rejection for the 3-node loop, got:\n%s", out)
		}
	})

	t.Run("legal_fanout_gate_dag_allowed", func(t *testing.T) {
		// A legitimate acyclic fanout-gate chain A→B→C (A waits for B, B waits
		// for C) must stay allowed — the reachability walk permits an acyclic
		// DAG while rejecting a cycle, exactly the beads-8ix02 supersede-chain
		// reasoning. This is the guard against a family walk being over-broad.
		a := bdCreate(t, bd, dir, "gate 1", "--type", "task")
		b := bdCreate(t, bd, dir, "gate 2", "--type", "task")
		c := bdCreate(t, bd, dir, "gate 3", "--type", "task")

		if out := bdDep(t, bd, dir, "add", a.ID, b.ID, "--type", "waits-for"); !strings.Contains(out, "Added") {
			t.Errorf("legal chain A→B waits-for should be allowed, got:\n%s", out)
		}
		if out := bdDep(t, bd, dir, "add", b.ID, c.ID, "--type", "waits-for"); !strings.Contains(out, "Added") {
			t.Errorf("legal chain B→C waits-for should be allowed, got:\n%s", out)
		}
	})
}
