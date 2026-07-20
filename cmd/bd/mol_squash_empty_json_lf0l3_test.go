//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedMolSquashEmptyJSONArray_lf0l3: `bd mol squash <mol> --json` on a
// molecule with NO ephemeral (wisp) children hits the len(wispChildren)==0 leg,
// which emitted a SquashResult with a nil SquashedIDs → "squashed_ids":null. An
// epic with only a persistent child reaches it. The fix inits SquashedIDs to []
// so the empty leg matches the success path's array contract (beads-lf0l3,
// guib/tamf null-slice class). RED before the fix (null); GREEN after ([]).
func TestEmbeddedMolSquashEmptyJSONArray_lf0l3(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ms")

	epic := bdCreate(t, bd, dir, "Squash empty epic", "--type", "epic")
	// A PERSISTENT (non-ephemeral) child → the molecule has zero wisp children.
	bdCreate(t, bd, dir, "Persistent child", "--type", "task", "--parent", epic.ID)

	cmd := exec.Command(bd, "mol", "squash", epic.ID, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no JSON object in mol squash output: %s", s)
	}
	body := s[start:]
	if strings.Contains(body, `"squashed_ids":null`) || strings.Contains(body, `"squashed_ids": null`) {
		t.Errorf("mol squash --json (no wisp children) emitted squashed_ids:null, want [] (beads-lf0l3): %s", body)
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(body), &obj); jerr != nil {
		t.Fatalf("mol squash --json not a JSON object: %v\n%s", jerr, body)
	}
	if v, ok := obj["squashed_ids"]; ok && v == nil {
		t.Errorf("squashed_ids is null, want [] (beads-lf0l3): %s", body)
	}
}
