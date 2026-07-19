//go:build cgo

package main

import (
	"os"
	"testing"
)

// beads-xcujl: `bd dep add A B` and `bd dep A --blocks B` document themselves as
// equivalent (dep.go help: "bd dep --blocks ... is equivalent to: bd dep add"),
// yet their --json output used DIFFERENT endpoint key names for the same stored
// edge — dep add → {issue_id, depends_on_id}; dep --blocks → {blocker_id,
// blocked_id}. A consumer scripting "add a dependency" then had to branch on
// which spelling it used. The established/canonical vocabulary is
// issue_id/depends_on_id (the types.Dependency model + dep list --json read
// shape), so the --blocks path is the outlier and is realigned here.
//
// These tests assert BOTH cmds emit issue_id/depends_on_id with the SAME values
// for the SAME edge (values, not just keys — the --blocks path binds
// issue_id=the blocked/depending issue, depends_on_id=the blocker). They fail
// before the realignment (old --blocks output has no issue_id key).
func TestEmbeddedDepBlocksJSONVocab_xcujl(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dv")

	// Edge 1 built via `dep --blocks`: blk1 blocks dep1  →  dep1 depends_on blk1.
	blk1 := bdCreate(t, bd, dir, "blocker one", "--type", "task")
	dep1 := bdCreate(t, bd, dir, "blocked one", "--type", "task")
	// Edge 2 built via `dep add`: add2 depends_on on2.
	add2 := bdCreate(t, bd, dir, "adder two", "--type", "task")
	on2 := bdCreate(t, bd, dir, "dependee two", "--type", "task")

	t.Run("blocks_added_uses_canonical_vocab", func(t *testing.T) {
		// `dep blk1 --blocks dep1` = blk1 blocks dep1 = dep1 depends_on blk1.
		m := bdDepJSON(t, bd, dir, blk1.ID, "--blocks", dep1.ID)
		if got := m["status"]; got != "added" {
			t.Fatalf("expected status=added, got %v (full: %v)", got, m)
		}
		// Canonical keys present.
		if _, ok := m["issue_id"]; !ok {
			t.Errorf("dep --blocks --json missing canonical key %q; got %v", "issue_id", keysOf(m))
		}
		if _, ok := m["depends_on_id"]; !ok {
			t.Errorf("dep --blocks --json missing canonical key %q; got %v", "depends_on_id", keysOf(m))
		}
		// Outlier keys gone.
		if _, ok := m["blocker_id"]; ok {
			t.Errorf("dep --blocks --json still leaks outlier key %q; got %v", "blocker_id", keysOf(m))
		}
		if _, ok := m["blocked_id"]; ok {
			t.Errorf("dep --blocks --json still leaks outlier key %q; got %v", "blocked_id", keysOf(m))
		}
		// Values must map correctly: issue_id = the depending/blocked issue (dep1),
		// depends_on_id = the blocker (blk1). Renaming the keys must NOT swap the
		// values.
		if got := m["issue_id"]; got != dep1.ID {
			t.Errorf("issue_id should be the blocked/depending issue %q, got %v", dep1.ID, got)
		}
		if got := m["depends_on_id"]; got != blk1.ID {
			t.Errorf("depends_on_id should be the blocker %q, got %v", blk1.ID, got)
		}
	})

	t.Run("blocks_matches_dep_add_vocab", func(t *testing.T) {
		// `dep add add2 on2` = add2 depends_on on2.
		m := bdDepJSON(t, bd, dir, "add", add2.ID, on2.ID)
		if got := m["status"]; got != "added" {
			t.Fatalf("expected status=added, got %v (full: %v)", got, m)
		}
		// dep add is the canonical baseline — issue_id=fromID(add2),
		// depends_on_id=toID(on2). The --blocks test above must use the SAME keys.
		if got := m["issue_id"]; got != add2.ID {
			t.Errorf("dep add issue_id should be %q, got %v", add2.ID, got)
		}
		if got := m["depends_on_id"]; got != on2.ID {
			t.Errorf("dep add depends_on_id should be %q, got %v", on2.ID, got)
		}
	})

	t.Run("blocks_unchanged_reuse_uses_canonical_vocab", func(t *testing.T) {
		// Re-adding the same --blocks edge hits the idempotent "unchanged" branch,
		// which had the same outlier keys.
		m := bdDepJSON(t, bd, dir, blk1.ID, "--blocks", dep1.ID)
		if got := m["status"]; got != "unchanged" {
			t.Fatalf("expected status=unchanged on re-add, got %v (full: %v)", got, m)
		}
		if _, ok := m["issue_id"]; !ok {
			t.Errorf("dep --blocks (unchanged) --json missing key %q; got %v", "issue_id", keysOf(m))
		}
		if _, ok := m["depends_on_id"]; !ok {
			t.Errorf("dep --blocks (unchanged) --json missing key %q; got %v", "depends_on_id", keysOf(m))
		}
		if _, ok := m["blocker_id"]; ok {
			t.Errorf("dep --blocks (unchanged) --json still leaks %q; got %v", "blocker_id", keysOf(m))
		}
		if got := m["issue_id"]; got != dep1.ID {
			t.Errorf("unchanged issue_id should be %q, got %v", dep1.ID, got)
		}
		if got := m["depends_on_id"]; got != blk1.ID {
			t.Errorf("unchanged depends_on_id should be %q, got %v", blk1.ID, got)
		}
	})
}

// keysOf is defined in graph_all_json_test.go (same package, cgo build).
