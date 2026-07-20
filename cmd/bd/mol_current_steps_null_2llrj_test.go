//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedMolCurrentStepsEmptyJSONArray_2llrj: `bd mol current --mol <id>
// --json` on a molecule with no child steps (getMoleculeProgress skips the root,
// leaving zero steps) emitted "steps":null because progress.Steps started as a
// nil `var steps []*StepStatus`. The fix inits it to []. A plain issue with no
// children is a valid --mol target and reaches the empty-steps case. RED before
// the fix (null); GREEN after ([]).
func TestEmbeddedMolCurrentStepsEmptyJSONArray_2llrj(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "mc")

	// A childless molecule root (no child steps).
	root := bdCreate(t, bd, dir, "Childless molecule", "--type", "epic")

	cmd := exec.Command(bd, "mol", "current", root.ID, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if strings.Contains(s, `"steps":null`) || strings.Contains(s, `"steps": null`) {
		t.Errorf("mol current <id> --json (childless) emitted steps:null, want [] (beads-2llrj): %s", s)
	}
	// The payload is a JSON array of molecule-progress objects; the first must
	// carry steps as an array, not null.
	start := strings.Index(s, "[")
	if start < 0 {
		t.Fatalf("no JSON array in mol current output: %s", s)
	}
	var arr []map[string]interface{}
	if jerr := json.Unmarshal([]byte(s[start:]), &arr); jerr != nil {
		t.Fatalf("mol current --json not a JSON array: %v\n%s", jerr, s)
	}
	if len(arr) > 0 {
		if v, ok := arr[0]["steps"]; ok && v == nil {
			t.Errorf("steps is null, want [] (beads-2llrj): %s", s)
		}
	}
}
