//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestFormulaShowJSON_ErrorPathEmitsStdoutObject is the error-contract teeth for
// the beads-rg0c sweep, formula.go leg. `bd formula show` honors the persistent
// --json on its success path (outputJSON) but its not-found error path returned
// SilentExit after printing to stderr — plain text on stderr, EMPTY stdout — so
// under --json a failure produced empty stdout, breaking JSON parsers. This is
// the SilentExit variant of the same empty-stdout-under-json class the sweep
// fixes elsewhere via HandleError->HandleErrorRespectJSON. The fix routes the
// --json branch through HandleErrorRespectJSON while keeping the search-paths
// hint for plain-text mode.
//
// LoadByName resolves a formula file by name (no DB/server), so a missing name
// is a deterministic, server-free error path.
func TestFormulaShowJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "formula", "show", "no_such_formula_xyz", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// Expected to FAIL (formula not found) — err != nil is fine.
	if err == nil {
		t.Fatalf("`formula show no_such_formula_xyz --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `formula show --json` — the error must be emitted as a JSON object on stdout (SilentExit/plain-text breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `formula show --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `formula show --json` stdout, got: %s", out)
	}
}
