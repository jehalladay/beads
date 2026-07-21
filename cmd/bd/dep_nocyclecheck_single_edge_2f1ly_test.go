//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedDepAddNoCycleCheckSingleEdge is the beads-2f1ly teeth: the
// documented `bd dep add ... --no-cycle-check` flag was a silent no-op on every
// SINGLE-edge add — read only to gate the redundant post-insert warning while
// the insert always ran the per-edge cycle check. Fix threads SkipCycleCheck
// into the single-edge insert AND runs the same final whole-graph check the bulk
// --file path runs (skip PER-EDGE for speed, never graph integrity — the
// documented contract, dep.go bulk block + flag help). So:
//   - PARENT-CHILD cycle (caught only by the family-aware PER-EDGE check, not by
//     the blocking-only final check) LANDS with the flag → proves the per-edge
//     check is now genuinely skipped (pre-fix: rejected = silent no-op).
//   - BLOCKS cycle is still rejected by the final whole-graph check even with the
//     flag → graph integrity preserved, consistent with bulk.
//   - self-loop rejected unconditionally (beads-jg2s).
//
// Mutation: revert dep.go to plain fromStore.AddDependency (drop the
// transact+SkipCycleCheck) → the parent-child with-flag SUCCEED case goes RED
// (per-edge check runs again = the pre-fix silent no-op).
func TestEmbeddedDepAddNoCycleCheckSingleEdge(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	depEdgeExists := func(t *testing.T, dir, from, to, typ string) bool {
		t.Helper()
		for _, d := range showDeps(t, bd, dir, from) {
			if d.ID == to && d.Type == typ {
				return true
			}
		}
		return false
	}

	// (1) parent-child cycle edge WITH the flag SUCCEEDS: A child-of B (edge
	//     A->B), then B child-of A (edge B->A) closes a parent-child cycle the
	//     family-aware per-edge check rejects — --no-cycle-check skips it and the
	//     blocking-only final check doesn't cover parent-child, so it lands.
	t.Run("positional_parentchild_cycle_with_flag_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nc1")
		a := bdCreate(t, bd, dir, "issue A", "--type", "task")
		b := bdCreate(t, bd, dir, "issue B", "--type", "task")
		bdDep(t, bd, dir, "add", a.ID, b.ID, "--type", "parent-child")
		bdDep(t, bd, dir, "add", b.ID, a.ID, "--type", "parent-child", "--no-cycle-check")
		if !depEdgeExists(t, dir, b.ID, a.ID, "parent-child") {
			t.Errorf("--no-cycle-check should have landed the parent-child cycle edge %s->%s", b.ID, a.ID)
		}
	})

	// (2) same parent-child cycle WITHOUT the flag: the per-edge family check
	//     hard-rejects it (default safety unchanged).
	t.Run("positional_parentchild_cycle_without_flag_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nc2")
		a := bdCreate(t, bd, dir, "issue A", "--type", "task")
		b := bdCreate(t, bd, dir, "issue B", "--type", "task")
		bdDep(t, bd, dir, "add", a.ID, b.ID, "--type", "parent-child")
		out := bdDepFail(t, bd, dir, "add", b.ID, a.ID, "--type", "parent-child")
		if !strings.Contains(out, "cycle") {
			t.Errorf("expected a cycle rejection without --no-cycle-check, got:\n%s", out)
		}
		if depEdgeExists(t, dir, b.ID, a.ID, "parent-child") {
			t.Errorf("the cycle-forming edge must NOT exist after a rejected add")
		}
	})

	// (3) a BLOCKS cycle is STILL rejected WITH the flag — the final whole-graph
	//     check gates the commit (skip per-edge for speed, never graph integrity,
	//     matching the bulk --file contract).
	t.Run("positional_blocks_cycle_with_flag_still_rejected_by_final_check", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nc3")
		a := bdCreate(t, bd, dir, "issue A", "--type", "task")
		b := bdCreate(t, bd, dir, "issue B", "--type", "task")
		bdDep(t, bd, dir, "add", b.ID, a.ID, "--type", "blocks")
		out := bdDepFail(t, bd, dir, "add", a.ID, b.ID, "--type", "blocks", "--no-cycle-check")
		if !strings.Contains(out, "cycle") {
			t.Errorf("blocks cycle must still be caught by the final whole-graph check even with --no-cycle-check, got:\n%s", out)
		}
		if depEdgeExists(t, dir, a.ID, b.ID, "blocks") {
			t.Errorf("the blocks cycle edge must NOT land (final check rolls back)")
		}
	})

	// (4) self-loop is rejected EVEN WITH --no-cycle-check (beads-jg2s).
	t.Run("self_loop_rejected_even_with_flag", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nc4")
		a := bdCreate(t, bd, dir, "issue A", "--type", "task")
		out := bdDepFail(t, bd, dir, "add", a.ID, a.ID, "--type", "blocks", "--no-cycle-check")
		if !strings.Contains(out, "self") {
			t.Errorf("self-loop must be rejected even with --no-cycle-check (beads-jg2s), got:\n%s", out)
		}
	})
}

// TestProxiedDepAddNoCycleCheckSingleEdge is the beads-2f1ly proxied teeth. The
// proxied single-edge dep-add (addDepProxiedOne) and --blocks
// (runDepBlocksProxiedServer) paths passed an empty BulkAddDepsOpts{}, so
// --no-cycle-check was a silent no-op over the hub too. Fix threads
// SkipPerEdgeCycleCheck.
//
// The proxied path routes through domain addBulk, whose contract (per the flag
// help) is "skip the PER-EDGE check for speed" — with SkipPerEdgeCycleCheck it
// still runs ONE final whole-graph check, but that final check is BLOCKING-only
// (isBlockingDep). So a PARENT-CHILD cycle is caught by the family-aware
// per-edge check (rejected WITHOUT the flag) but NOT by the blocking-only final
// check (lands WITH the flag) — the clean observable signal that the flag is now
// threaded. (A pure blocks cycle stays rejected on the proxied path even with
// the flag, by the final whole-graph guard — graph integrity is preserved, which
// is correct; the direct single-edge path fully skips, covered above.) Self-loop
// stays unconditional (beads-jg2s). Mutation: drop SkipPerEdgeCycleCheck from the
// proxied single add → the with-flag SUCCEED case goes RED (per-edge check runs).
func TestProxiedDepAddNoCycleCheckSingleEdge(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	hasUpDep := func(t *testing.T, dir, parent, child string) bool {
		t.Helper()
		out := bdProxiedDep(t, bd, dir, "list", parent, "--direction", "up")
		return strings.Contains(out, child)
	}

	// (1) proxied parent-child cycle edge WITH the flag SUCCEEDS: A child-of B
	//     (edge A->B), then B child-of A (edge B->A) closes a parent-child cycle
	//     the per-edge family check rejects — but --no-cycle-check skips it and
	//     the blocking-only final check does not cover parent-child, so it lands.
	t.Run("proxied_parentchild_cycle_edge_with_flag_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pnc1")
		a := bdProxiedCreate(t, bd, p.dir, "Issue A")
		b := bdProxiedCreate(t, bd, p.dir, "Issue B")
		bdProxiedDep(t, bd, p.dir, "add", a.ID, b.ID, "--type", "parent-child")
		bdProxiedDep(t, bd, p.dir, "add", b.ID, a.ID, "--type", "parent-child", "--no-cycle-check")
		// b->a parent-child: a is a dependent (parent) of b → a's up-list has b.
		if !hasUpDep(t, p.dir, a.ID, b.ID) {
			t.Errorf("proxied --no-cycle-check should have landed the parent-child cycle edge %s->%s", b.ID, a.ID)
		}
	})

	// (2) same parent-child cycle WITHOUT the flag: the per-edge family check
	//     hard-rejects it (default safety unchanged).
	t.Run("proxied_parentchild_cycle_edge_without_flag_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pnc2")
		a := bdProxiedCreate(t, bd, p.dir, "Issue A")
		b := bdProxiedCreate(t, bd, p.dir, "Issue B")
		bdProxiedDep(t, bd, p.dir, "add", a.ID, b.ID, "--type", "parent-child")
		out := bdProxiedDepFail(t, bd, p.dir, "add", b.ID, a.ID, "--type", "parent-child")
		if !strings.Contains(out, "cycle") {
			t.Errorf("expected a cycle rejection on proxied parent-child add without the flag, got:\n%s", out)
		}
	})

	// (3) a pure BLOCKS cycle stays rejected on the proxied path EVEN WITH the
	//     flag — the blocking-only final whole-graph check preserves integrity
	//     (documented bulk contract: skip per-edge for speed, never graph
	//     integrity). Direct single-edge fully skips (covered in the embedded test).
	t.Run("proxied_blocks_cycle_with_flag_still_rejected_by_final_check", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pnc3")
		a := bdProxiedCreate(t, bd, p.dir, "Issue A")
		b := bdProxiedCreate(t, bd, p.dir, "Issue B")
		bdProxiedDep(t, bd, p.dir, "add", b.ID, a.ID, "--type", "blocks")
		out := bdProxiedDepFail(t, bd, p.dir, "add", a.ID, b.ID, "--type", "blocks", "--no-cycle-check")
		if !strings.Contains(out, "cycle") {
			t.Errorf("proxied blocks cycle must still be caught by the final whole-graph check even with --no-cycle-check, got:\n%s", out)
		}
	})

	// (4) self-loop rejected even with the flag on the proxied path (beads-jg2s).
	t.Run("proxied_self_loop_rejected_even_with_flag", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pnc4")
		a := bdProxiedCreate(t, bd, p.dir, "Issue A")
		out := bdProxiedDepFail(t, bd, p.dir, "add", a.ID, a.ID, "--type", "blocks", "--no-cycle-check")
		if !strings.Contains(out, "self") {
			t.Errorf("proxied self-loop must be rejected even with --no-cycle-check, got:\n%s", out)
		}
	})
}
