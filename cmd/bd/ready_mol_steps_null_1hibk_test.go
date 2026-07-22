//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedReadyMolStepsEmptyJSONArray_1hibk: `bd ready --mol <id> --json` on
// a molecule with NO ready steps emitted "steps":null because both emit sites
// (ready.go direct + ready_proxied_server.go proxied) built readySteps as a
// naked `var readySteps []*MoleculeReadyStep` and only appended when a step's
// IsReady. When every issue is closed (or all open steps are blocked), the loop
// appends nothing and the nil slice marshals to null — while the sibling field
// ParallelGroups (make(map...) in analyzeMoleculeParallel) always emits {}. The
// fix inits readySteps to []. Same nil-slice-array contract as beads-2llrj
// (mol current) and beads-1sq7f (mol show). This branch is REACHABLE (unlike the
// mol_burn 5hpw0 empty-guards): a molecule whose steps are all closed loads a
// non-empty subgraph but yields zero ready steps. RED before (null); GREEN after.
func TestEmbeddedReadyMolStepsEmptyJSONArray_1hibk(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rm")

	// A molecule with two steps, then close everything so no step is ready.
	mol := bdCreate(t, bd, dir, "Done molecule", "--type", "molecule")
	s1 := bdCreate(t, bd, dir, "Step one", "--parent", mol.ID)
	s2 := bdCreate(t, bd, dir, "Step two", "--parent", mol.ID)
	bdClose(t, bd, dir, s1.ID)
	bdClose(t, bd, dir, s2.ID)
	bdClose(t, bd, dir, mol.ID)

	cmd := exec.Command(bd, "ready", "--mol", mol.ID, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))

	if strings.Contains(s, `"steps":null`) || strings.Contains(s, `"steps": null`) {
		t.Errorf("bd ready --mol <id> --json (no ready steps) emitted steps:null, want [] (beads-1hibk): %s", s)
	}

	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no JSON object in ready --mol output: %s", s)
	}
	var out2 MoleculeReadyOutput
	if err := json.Unmarshal([]byte(s[start:]), &out2); err != nil {
		t.Fatalf("parse MoleculeReadyOutput: %v\n%s", err, s[start:])
	}
	// The typed decode makes a nil slice indistinguishable from []; the string
	// check above is the real tooth. Assert the semantic invariant too.
	if out2.ReadySteps != 0 {
		t.Errorf("ReadySteps = %d, want 0 for an all-closed molecule", out2.ReadySteps)
	}
	if out2.Steps == nil {
		// json.Unmarshal leaves Steps nil for both null and [], so this only
		// fires if the raw check above already passed a []; kept as a guard.
		t.Logf("note: decoded Steps is nil (raw string check is authoritative): %s", s)
	}
}

// TestProxiedReadyMolStepsEmptyJSONArray_1hibk mirrors the above through the
// proxied-server path (ready_proxied_server.go emit site), which had the same
// naked `var readySteps` nil-slice.
func TestProxiedReadyMolStepsEmptyJSONArray_1hibk(t *testing.T) {
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "rmp")

	mol := bdProxiedCreate(t, bd, p.dir, "Done molecule", "--type", "molecule")
	s1 := bdProxiedCreate(t, bd, p.dir, "Step one", "--parent", mol.ID)
	s2 := bdProxiedCreate(t, bd, p.dir, "Step two", "--parent", mol.ID)
	if _, err := bdProxiedRun(t, bd, p.dir, "close", s1.ID); err != nil {
		t.Fatalf("close %s: %v", s1.ID, err)
	}
	if _, err := bdProxiedRun(t, bd, p.dir, "close", s2.ID); err != nil {
		t.Fatalf("close %s: %v", s2.ID, err)
	}
	if _, err := bdProxiedRun(t, bd, p.dir, "close", mol.ID); err != nil {
		t.Fatalf("close %s: %v", mol.ID, err)
	}

	stdout, _, err := bdProxiedRunBuffers(t, bd, p.dir, "ready", "--mol", mol.ID, "--json")
	if err != nil {
		t.Fatalf("bd ready --mol --json failed: %v\n%s", err, stdout)
	}
	s := strings.TrimSpace(stdout)
	if strings.Contains(s, `"steps":null`) || strings.Contains(s, `"steps": null`) {
		t.Errorf("proxied bd ready --mol <id> --json (no ready steps) emitted steps:null, want [] (beads-1hibk): %s", s)
	}
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no JSON object in proxied ready --mol output: %s", s)
	}
	var out MoleculeReadyOutput
	if err := json.Unmarshal([]byte(s[start:]), &out); err != nil {
		t.Fatalf("parse MoleculeReadyOutput: %v\n%s", err, s[start:])
	}
	if out.ReadySteps != 0 {
		t.Errorf("ReadySteps = %d, want 0 for an all-closed molecule", out.ReadySteps)
	}
}
