//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDepAddRefusesRelatesTo_hf1c6 covers beads-hf1c6: `bd dep add --type
// relates-to A B` would mint a one-sided A->B edge, bypassing the bidirectional
// relates-to invariant (bd dep relate writes both directions atomically). Per
// the invariant-owner separation (add/remove are directional primitives;
// relate/unrelate own bidirectional relates-to — same reasoning as the xlplm
// dep-remove refusal), dep add REFUSES relates-to (rc1, redirect to dep relate)
// and mints NO edge, so the asymmetric link ri535 heals can never be created
// via this forward CLI path.
func TestDepAddRefusesRelatesTo_hf1c6(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "da")

	a := bdCreate(t, bd, dir, "relates add A", "--type", "task")
	b := bdCreate(t, bd, dir, "relates add B", "--type", "task")

	// `bd dep add --type relates-to A B` must FAIL LOUD + redirect.
	out := bdDepFail(t, bd, dir, "add", a.ID, b.ID, "--type", "relates-to")
	if strings.Contains(out, "Added") {
		t.Errorf("dep add --type relates-to falsely reported 'Added' (beads-hf1c6):\n%s", out)
	}
	if !strings.Contains(out, "dep relate") {
		t.Errorf("dep add --type relates-to should redirect to 'bd dep relate', got:\n%s", out)
	}

	// NO edge may have been minted in EITHER direction (the refuse is before the write).
	depJSONA := bdDep(t, bd, dir, "list", a.ID, "--json")
	if strings.Contains(depJSONA, b.ID) {
		t.Errorf("dep add --type relates-to minted a one-sided edge %s -> %s despite the refusal (beads-hf1c6):\n%s", a.ID, b.ID, depJSONA)
	}
	depJSONB := bdDep(t, bd, dir, "list", b.ID, "--json")
	if strings.Contains(depJSONB, a.ID) {
		t.Errorf("dep add --type relates-to minted an edge %s -> %s (beads-hf1c6):\n%s", b.ID, a.ID, depJSONB)
	}

	// Control 1: dep add on a real 'blocks' edge still works (not over-refused).
	c := bdCreate(t, bd, dir, "blocks add C", "--type", "task")
	d := bdCreate(t, bd, dir, "blocks add D", "--type", "task")
	rmOut := bdDep(t, bd, dir, "add", c.ID, d.ID, "--type", "blocks")
	if !strings.Contains(rmOut, "Added") {
		t.Errorf("dep add --type blocks should still work, got:\n%s", rmOut)
	}

	// Control 2: the correct verb, bd dep relate, still creates a bidirectional link.
	rel := exec.Command(bd, "dep", "relate", a.ID, b.ID)
	rel.Dir = dir
	rel.Env = bdEnv(dir)
	if o, err := rel.CombinedOutput(); err != nil {
		t.Fatalf("bd dep relate (the redirect target) failed: %v\n%s", err, o)
	}
	// Both directions now present (relate is bidirectional).
	if !strings.Contains(bdDep(t, bd, dir, "list", a.ID, "--json"), b.ID) ||
		!strings.Contains(bdDep(t, bd, dir, "list", b.ID, "--json"), a.ID) {
		t.Errorf("bd dep relate did not create the expected bidirectional link after the dep-add refusal")
	}
}
