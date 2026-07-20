//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDepRemoveRefusesRelatesTo_xlplm covers beads-xlplm: `bd dep remove` on a
// relates-to edge would delete only one direction, orphaning the reciprocal
// (relates-to is bidirectional — bd relate/unrelate own it). Per the ruling
// (A), dep remove REFUSES a relates-to link (rc1, redirect to bd unrelate) and
// leaves BOTH edges intact, keeping dep-remove a directional-types primitive
// with no duplicated bidirectional logic to drift.
func TestDepRemoveRefusesRelatesTo_xlplm(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dr")

	a := bdCreate(t, bd, dir, "relates A", "--type", "task")
	b := bdCreate(t, bd, dir, "relates B", "--type", "task")

	// Seed a bidirectional relates-to link via bd dep relate.
	rel := exec.Command(bd, "dep", "relate", a.ID, b.ID)
	rel.Dir = dir
	rel.Env = bdEnv(dir)
	if out, err := rel.CombinedOutput(); err != nil {
		t.Fatalf("seed relate failed: %v\n%s", err, out)
	}

	// `bd dep remove A B` on the relates-to edge must FAIL LOUD + redirect.
	out := bdDepFail(t, bd, dir, "remove", a.ID, b.ID)
	if strings.Contains(out, "Removed") {
		t.Errorf("dep remove on a relates-to link falsely reported 'Removed' (beads-xlplm):\n%s", out)
	}
	if !strings.Contains(out, "unrelate") {
		t.Errorf("dep remove on a relates-to link should redirect to 'bd unrelate', got:\n%s", out)
	}

	// BOTH reciprocal edges must remain intact (not orphaned).
	depJSON := bdDep(t, bd, dir, "list", a.ID, "--json")
	if !strings.Contains(depJSON, b.ID) {
		t.Errorf("relates-to edge %s -> %s was removed despite the refusal (beads-xlplm):\n%s", a.ID, b.ID, depJSON)
	}
	depJSONB := bdDep(t, bd, dir, "list", b.ID, "--json")
	if !strings.Contains(depJSONB, a.ID) {
		t.Errorf("reciprocal relates-to edge %s -> %s was orphaned (beads-xlplm):\n%s", b.ID, a.ID, depJSONB)
	}

	// Control: dep remove on a real 'blocks' edge still works (not over-refused).
	c := bdCreate(t, bd, dir, "blocks C", "--type", "task")
	d := bdCreate(t, bd, dir, "blocks D", "--type", "task")
	bdDep(t, bd, dir, "add", c.ID, d.ID) // default type: blocks
	rmOut := bdDep(t, bd, dir, "remove", c.ID, d.ID)
	if !strings.Contains(rmOut, "Removed") {
		t.Errorf("dep remove on a 'blocks' edge should still work, got:\n%s", rmOut)
	}
}
