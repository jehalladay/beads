//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestFormulaConvertJSON_ErrorPathEmitsStdoutObject is the error-contract teeth
// for beads-e0o3d, formula.go runFormulaConvert leg. `bd formula convert` honors
// the persistent --json on its success path but its not-found error path did a
// bare fmt.Fprintf(os.Stderr, ...) + SilentExit (WORSE than HandleError — a raw
// stderr write) — plain text on stderr, EMPTY stdout — so under --json a failure
// produced empty stdout, breaking JSON parsers. This mirrors the sibling
// `formula show` fix (@207): route the --json branch through
// HandleErrorRespectJSON (folding the search-paths hint into the error string)
// while keeping the multi-line search-paths listing for plain-text mode.
//
// findFormulaJSON resolves a formula by name against the search paths (no
// DB/server), so a missing name is a deterministic, server-free error path.
func TestFormulaConvertJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "formula", "convert", "no_such_formula_xyz", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// Expected to FAIL (formula not found) — err != nil is fine.
	if err == nil {
		t.Fatalf("`formula convert no_such_formula_xyz --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `formula convert --json` — the error must be emitted as a JSON object on stdout (bare Fprintf+SilentExit breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `formula convert --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `formula convert --json` stdout, got: %s", out)
	}
}

// TestFormulaConvertAlreadyTOMLJSON_kqpih is the completeness teeth for
// beads-kqpih: e0o3d converted the parse/convert/write/not-found legs of
// runFormulaConvert to the --json error contract but left the
// "<name> is already a TOML file" leg (reached when the arg ends in .toml) on a
// bare HandleError, so `bd formula convert foo.toml --json` leaked plaintext to
// stderr with EMPTY stdout. After the fix it must emit a JSON error on stdout.
func TestFormulaConvertAlreadyTOMLJSON_kqpih(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "formula", "convert", "already.formula.toml", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("`formula convert already.formula.toml --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on `formula convert already.formula.toml --json` — the 'already a TOML file' error must be a JSON object on stdout (beads-kqpih)\nstderr:\n%s", stderr.String())
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on `formula convert already.formula.toml --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || !strings.Contains(msg, "already a TOML file") {
		t.Errorf("expected an \"error\" field mentioning 'already a TOML file', got: %s", out)
	}
}
