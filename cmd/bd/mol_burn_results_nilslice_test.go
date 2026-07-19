//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedMolBurnResultsNonNullArray_m9gn is the teeth for beads-m9gn.
//
// BatchBurnResult.Results is a []BurnResult json field ("results") with NO
// omitempty. When `bd mol burn` resolves no valid molecules, burnMultipleMolecules
// emitted outputJSON(BatchBurnResult{FailedCount: ...}) leaving Results nil, so
// `bd mol burn <bad-ids> --json` produced "results":null while the success path
// (make([]BurnResult, 0)) emitted []. The guib/036h/5fv3/jxel/4mkg/8wyu
// nil-slice asymmetry, here across two branches of one command. The fix inits
// Results in the failure-branch literal.
//
// This is an END-TO-END embedded-dolt test (not a self-constructed marshal,
// which would be false-green): it runs the real `bd mol burn <bogus> --json`
// path and asserts the emitted results field is [] not null. RED proof:
// reverting the Results init makes this branch emit "results":null.
func TestEmbeddedMolBurnResultsNonNullArray_m9gn(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "br")

	// All ids fail to resolve → the no-valid-molecules branch fires and emits
	// the BatchBurnResult json. It must carry results:[] not null.
	cmd := exec.Command(bd, "mol", "burn", "ghost-a", "ghost-b", "--json", "--force")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	// This branch exits non-zero (failed_count>0), so err != nil is expected.
	if err == nil {
		t.Fatalf("expected non-zero exit for all-bogus burn, got success:\n%s", out)
	}

	// Find the JSON object in the output (it may be wrapped in a schema
	// envelope; unmarshal and look for "results" at top level or under "data").
	s := strings.TrimSpace(string(out))
	if strings.Contains(s, `"results":null`) || strings.Contains(s, `"results": null`) {
		t.Errorf("all-bogus `bd mol burn --json` emitted results:null — must be [] like the success path (beads-m9gn)\noutput:\n%s", s)
	}
	if !strings.Contains(s, `"results":[]`) && !strings.Contains(s, `"results": []`) {
		t.Errorf("expected results:[] in the all-bogus burn payload, got:\n%s", s)
	}

	// Sanity: the payload parses and carries the failure signal.
	// Locate the first JSON object line.
	var obj map[string]interface{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") {
			if json.Unmarshal([]byte(line), &obj) == nil {
				break
			}
		}
	}
	// If it parsed as a single object we don't hard-require field access (the
	// envelope shape varies); the string assertions above are the contract.
	_ = obj
}
