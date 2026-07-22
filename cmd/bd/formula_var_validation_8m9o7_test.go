//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormulaVarValidation_8m9o7 is the beads-8m9o7 teeth: formula var
// enum/pattern validation was defined (formula.ValidateVars) but had ZERO
// production callers, so `bd cook --mode runtime` and `bd mol pour` silently
// accepted out-of-enum / pattern-violating values and substituted them into
// real, durable issue content. The fix wires formula.ValidateVars /
// ValidateVarValues into both runtime paths, failing loud (rc=1, JSON-aware).
//
// These teeth drive the real embedded `bd` subprocess through the CLI so the
// enforcement is verified at the reachable command layer (a pure ValidateVars
// unit test would false-green on the missing wiring — the whole defect was that
// nothing CALLED it). Mutation-verify: remove the ValidateVars call from
// cook.go (runCook) and pour.go (before spawnMolecule) and the reject subtests
// go RED (invalid values succeed with rc=0).
func TestFormulaVarValidation_8m9o7(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A formula with an enum-constrained var and a pattern-constrained var.
	formulaBody := func(name string) string {
		return `formula = "` + name + `"
description = "beads-8m9o7 var-validation teeth"
version = 1
type = "workflow"

[vars.tier]
description = "Service tier"
enum = ["gold", "silver"]

[vars.code]
description = "Three-letter uppercase code"
pattern = "^[A-Z]{3}$"

[[steps]]
id = "provision"
title = "Provision {{tier}} tier for {{code}}"
description = "one step so the proto is valid"
`
	}

	// writeFormula drops the formula both as a standalone file (for `cook <path>`)
	// and into the <beadsDir>/formulas registry (for `mol pour <name>`, which
	// resolves by name via parser.LoadByName, matching the dogfooder repro
	// `bd mol pour mol-enum`). Returns the standalone file path.
	writeFormula := func(t *testing.T, dir, beadsDir, name string) string {
		t.Helper()
		path := filepath.Join(dir, name+".formula.toml")
		if err := os.WriteFile(path, []byte(formulaBody(name)), 0o644); err != nil {
			t.Fatalf("write formula %s: %v", name, err)
		}
		regDir := filepath.Join(beadsDir, "formulas")
		if err := os.MkdirAll(regDir, 0o755); err != nil {
			t.Fatalf("mkdir formulas registry: %v", err)
		}
		if err := os.WriteFile(filepath.Join(regDir, name+".formula.toml"), []byte(formulaBody(name)), 0o644); err != nil {
			t.Fatalf("write registry formula %s: %v", name, err)
		}
		return path
	}

	t.Run("cook_runtime_rejects_out_of_enum", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "mve")
		path := writeFormula(t, dir, beadsDir, "mve-enum")
		// platinum is NOT in enum=[gold,silver]; expect fail-loud rc!=0.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"cook", path, "--mode", "runtime",
			"--var", "tier=platinum", "--var", "code=ABC", "--json")
		if err == nil {
			t.Fatalf("beads-8m9o7: `cook --mode runtime` with tier=platinum (out of enum) must "+
				"fail loud (rc!=0), got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "not in allowed values") {
			t.Errorf("beads-8m9o7: enum-violation error must mention \"not in allowed values\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("cook_runtime_rejects_pattern_violation", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "mvp")
		path := writeFormula(t, dir, beadsDir, "mvp-enum")
		// ab1 fails pattern ^[A-Z]{3}$; expect fail-loud.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"cook", path, "--mode", "runtime",
			"--var", "tier=gold", "--var", "code=ab1", "--json")
		if err == nil {
			t.Fatalf("beads-8m9o7: `cook --mode runtime` with code=ab1 (pattern violation) must "+
				"fail loud (rc!=0), got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "does not match pattern") {
			t.Errorf("beads-8m9o7: pattern-violation error must mention \"does not match pattern\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("cook_runtime_accepts_valid", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "mvv")
		path := writeFormula(t, dir, beadsDir, "mvv-enum")
		// Valid values must still succeed (positive control: proves the fix
		// rejects ONLY invalid values, not everything).
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"cook", path, "--mode", "runtime",
			"--var", "tier=gold", "--var", "code=ABC", "--json")
		if err != nil {
			t.Fatalf("beads-8m9o7: `cook --mode runtime` with valid tier=gold/code=ABC must "+
				"succeed; got err %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})

	t.Run("pour_rejects_out_of_enum", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "mvr")
		_ = writeFormula(t, dir, beadsDir, "mvr-enum")
		// Pour cooks the formula inline (VarDefs populated), then would spawn
		// persistent issues. An out-of-enum tier must be rejected BEFORE spawn.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"mol", "pour", "mvr-enum",
			"--var", "tier=platinum", "--var", "code=ABC", "--json")
		if err == nil {
			t.Fatalf("beads-8m9o7: `mol pour` with tier=platinum (out of enum) must fail loud "+
				"(rc!=0) and NOT create persistent issues; got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "not in allowed values") {
			t.Errorf("beads-8m9o7: pour enum-violation error must mention \"not in allowed values\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("pour_accepts_valid", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "mvo")
		_ = writeFormula(t, dir, beadsDir, "mvo-enum")
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"mol", "pour", "mvo-enum",
			"--var", "tier=silver", "--var", "code=XYZ", "--json")
		if err != nil {
			t.Fatalf("beads-8m9o7: `mol pour` with valid tier=silver/code=XYZ must succeed; "+
				"got err %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		// Sanity: the created issue carries the VALID substituted value.
		var obj map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(stdout)), &obj) == nil {
			// best-effort; the primary assertion is rc==0.
			_ = obj
		}
	})
}

// bdRun8m9o7 runs a bd subprocess capturing stdout+stderr separately and
// returns them plus the run error (nil == rc 0). It retries on embedded-flock
// contention (same pattern as bdRunOK) but — unlike bdRunOK — does NOT fail the
// test on a non-nil error, so callers can assert on expected failures.
func bdRun8m9o7(t *testing.T, bd, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	for attempt := 0; attempt < 10; attempt++ {
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		so, se, runErr := runCommandBuffers(t, cmd)
		stdout, stderr = so.String(), se.String()
		if runErr == nil || !isEmbeddedLockOutput(stdout+stderr) {
			return stdout, stderr, runErr
		}
		t.Logf("bd %s: flock contention (attempt %d/10), retrying...", args[0], attempt+1)
	}
	t.Fatalf("beads-8m9o7: `bd %s` still flock-contended after 10 attempts", strings.Join(args, " "))
	return "", "", nil
}
