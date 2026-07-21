//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestFederationSyncFalseSuccess_o35h0 is the teeth for beads-o35h0:
// `bd federation sync` returned RC=0 on a per-peer sync FAILURE and hid the
// error in --json.
//
// runFederationSync's per-peer loop printed "✗ <err>" (human-only) then
// `continue`d, and the function ended with an unconditional `return nil` — so a
// failed sync exited 0. And SyncResult.Error is `json:"-"` (an error marshals to
// {}), while the human ✗ print is !jsonOutput-gated, so --json emitted only
// {merged:false} with NO failure signal. Federation sync is the multi-town data
// path; a false-success exit code + error-less JSON means automation cannot
// detect a failed sync.
//
// Repro: add a peer pointing at a non-existent local URL, then sync — the fetch
// fails, so the merge never happens. The fix must (a) exit non-zero and (b)
// surface the error string + a top-level `failed:true` in --json.
func TestFederationSyncFalseSuccess_o35h0(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt federation tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "o35h0")

	// A peer that cannot be fetched from (no repo at this path) → sync fails.
	badURL := "file://" + t.TempDir() + "/does-not-exist"
	bdFederation(t, bd, dir, "add-peer", "towna", badURL)

	// runSync executes `bd federation sync` with the given extra args and
	// returns combined output + the exit code (0 on success).
	runSync := func(extra ...string) (string, int) {
		t.Helper()
		args := append([]string{"federation", "sync", "--peer", "towna"}, extra...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatalf("federation sync exec error (not an exit error): %v\n%s", err, out)
			}
		}
		return string(out), code
	}

	// ── HUMAN leg: a failed sync must exit non-zero (was RC=0). ──
	humanOut, humanCode := runSync()
	if !strings.Contains(humanOut, "✗") {
		t.Fatalf("expected a ✗ failure line for a failed sync; got:\n%s", humanOut)
	}
	if humanCode == 0 {
		t.Errorf("o35h0: `bd federation sync` on a FAILED peer sync must exit non-zero; got RC=0\n%s", humanOut)
	}

	// ── JSON leg: the failure must be visible (error string + failed:true). ──
	jsonOut, jsonCode := runSync("--json")
	if jsonCode == 0 {
		t.Errorf("o35h0: `bd federation sync --json` on a FAILED sync must exit non-zero; got RC=0\n%s", jsonOut)
	}
	var payload struct {
		Failed  bool `json:"failed"`
		Results []struct {
			Merged bool   `json:"merged"`
			Error  string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("federation sync --json should emit valid JSON: %v\n%s", err, jsonOut)
	}
	if !payload.Failed {
		t.Errorf("o35h0: --json must carry top-level failed:true on a failed sync; got:\n%s", jsonOut)
	}
	if len(payload.Results) == 0 {
		t.Fatalf("expected at least one result in the JSON; got:\n%s", jsonOut)
	}
	if payload.Results[0].Merged {
		t.Errorf("failed sync result should not report merged:true; got:\n%s", jsonOut)
	}
	if payload.Results[0].Error == "" {
		t.Errorf("o35h0: the failed result must carry a non-empty `error` string (Error was json:\"-\"); got:\n%s", jsonOut)
	}
}
