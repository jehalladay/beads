//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEmbeddedPourVaporJSONStderr is the beads-8zed6 teeth (8lqh json-error
// contract, Direction-2 interleaved-stderr class).
//
// runPour emits a pourResult JSON success envelope on stdout under --json, but
// when the resolved formula declares phase="vapor" it ALSO wrote a 6-line
// plaintext advisory ("⚠ Formula %q recommends vapor phase..." + "Consider
// using: bd mol wisp..." + ...) to os.Stderr UNCONDITIONALLY (pour.go:160-177).
// Under `bd mol pour <vapor-formula> --json 2>&1 | jq` that plaintext
// interleaves with the JSON object -> parse failure. The fix gates the advisory
// behind `!jsonOutput` (matching the sibling advisories at mol_burn.go /
// migrate_issues.go).
//
// This drives the real embedded `bd mol pour` subprocess against a persisted
// vapor-phase formula and asserts the COMBINED (2>&1) stream is a single
// parseable JSON object. Because the defect is a stderr write, the teeth MUST
// capture stderr — the flock helper that returns stdout-only would false-green.
//
// Mutation-verify: restore the unconditional `if sg.Phase == "vapor" {` (drop
// the `&& !jsonOutput`) and vapor_pour_json_is_pure goes RED (the leading
// plaintext advisory trips json.Unmarshal on the combined 2>&1 stream).
func TestEmbeddedPourVaporJSONStderr(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// (1) The bug: pouring a vapor-phase formula with --json must NOT leak the
	//     plaintext advisory into the 2>&1 stream — the COMBINED output must
	//     parse as a single JSON object. Positive-control guard below asserts
	//     the vapor branch is actually reachable, so this can't false-green.
	t.Run("vapor_pour_json_is_pure", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "vpa")
		writeVaporFormula(t, beadsDir, "vpa-vapor")

		stdout, stderr := runPourVaporWithFlockRetry(t, bd, dir, "mol", "pour", "vpa-vapor", "--json")
		// The real `--json 2>&1 | jq` stream is stdout+stderr concatenated.
		combined := strings.TrimSpace(stdout + stderr)
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(combined), &obj); jerr != nil {
			t.Fatalf("beads-8zed6: `bd mol pour <vapor> --json` 2>&1 is NOT a single JSON object "+
				"(vapor advisory leaked plaintext to stderr): %v\ncombined:\n%s", jerr, combined)
		}
		// Sanity: the success envelope is present (this really was a pour, not
		// an error object) so the test can't false-green on an error JSON.
		if _, ok := obj["phase"]; !ok {
			t.Errorf("beads-8zed6: parsed JSON lacks the pourResult 'phase' field; got: %s", combined)
		}
	})

	// (2) Positive control: WITHOUT --json the human advisory is still emitted
	//     to stderr. This proves the vapor branch is genuinely REACHABLE with
	//     this formula setup — without it, subtest (1) would pass vacuously (the
	//     advisory never fires, so nothing leaks regardless of the guard). The
	//     fix suppresses the advisory ONLY under --json; it must not delete the
	//     hint from the interactive path.
	t.Run("vapor_pour_plain_keeps_advisory", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "vpb")
		writeVaporFormula(t, beadsDir, "vpb-vapor")

		stdout, stderr := runPourVaporWithFlockRetry(t, bd, dir, "mol", "pour", "vpb-vapor")
		combined := stdout + stderr
		if !strings.Contains(combined, "recommends vapor phase") {
			t.Errorf("beads-8zed6: plain (non-json) pour of a vapor formula must still print "+
				"the vapor advisory to stderr (fix suppresses only under --json); "+
				"stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})
}

// runPourVaporWithFlockRetry runs a bd subprocess capturing stdout and stderr
// SEPARATELY (unlike bdRunWithFlockRetry, which discards stderr on success),
// retrying on embedded-Dolt flock contention. The 8zed6 defect is a stderr
// write, so the teeth must see stderr — returning stdout-only would false-green.
func runPourVaporWithFlockRetry(t *testing.T, bd, dir string, args ...string) (stdout, stderr string) {
	t.Helper()
	for attempt := 0; attempt < 10; attempt++ {
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		so, se, err := runCommandBuffers(t, cmd)
		stdout, stderr = so.String(), se.String()
		if err == nil {
			return stdout, stderr
		}
		if !isEmbeddedLockOutput(stdout + stderr) {
			t.Fatalf("beads-8zed6: `bd %s` failed: %v\nstdout:\n%s\nstderr:\n%s",
				strings.Join(args, " "), err, stdout, stderr)
		}
		t.Logf("bd %s: flock contention (attempt %d/10), retrying...", args[0], attempt+1)
		time.Sleep(time.Duration(500*(1<<min(attempt, 4))) * time.Millisecond)
	}
	t.Fatalf("beads-8zed6: `bd %s` still flock-contended after 10 attempts\nstdout:\n%s\nstderr:\n%s",
		strings.Join(args, " "), stdout, stderr)
	return stdout, stderr
}

// writeVaporFormula writes a minimal single-step workflow formula that declares
// phase="vapor" into the project formulas dir, so `bd mol pour <name>` resolves
// it by bare name (resolveAndCookFormulaWithVars) and reaches the vapor-advisory
// branch (pour.go: sg.Phase == "vapor").
func writeVaporFormula(t *testing.T, beadsDir, name string) {
	t.Helper()
	formulasDir := filepath.Join(beadsDir, "formulas")
	if err := os.MkdirAll(formulasDir, 0o755); err != nil {
		t.Fatalf("mkdir formulas dir: %v", err)
	}
	body := "formula = \"" + name + "\"\n" +
		"description = \"beads-8zed6 vapor-phase advisory teeth\"\n" +
		"version = 1\n" +
		"type = \"workflow\"\n" +
		"phase = \"vapor\"\n\n" +
		"[[steps]]\n" +
		"id = \"only\"\n" +
		"title = \"only step\"\n" +
		"description = \"single step so the cooked proto materializes\"\n"
	path := filepath.Join(formulasDir, name+".formula.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write vapor formula %s: %v", name, err)
	}
}
