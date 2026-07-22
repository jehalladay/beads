//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormulaVarTypeValidation_w5sk7 is the beads-w5sk7 teeth: formula
// VarDef.Type ("string"/"int"/"bool") was declared (types.go) but never
// enforced by formula.ValidateVarValues — a var declared type:int silently
// accepted a non-numeric value (e.g. "foo") and substituted it into real,
// durable issue content. This STACKS on beads-8m9o7 (which wired
// ValidateVarValues into the cook+pour runtime paths); w5sk7 adds the type
// coercion check inside that same shared body, so both runtime paths inherit
// it automatically.
//
// These teeth drive the real embedded `bd` subprocess through the CLI so the
// enforcement is verified at the reachable command layer (a pure ValidateVars
// unit test would false-green on the missing wiring). Mutation-verify: remove
// the type-check switch in ValidateVarValues (parser.go) and the reject
// subtests go RED (invalid int/bool values succeed with rc=0).
func TestFormulaVarTypeValidation_w5sk7(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A formula with an int-typed var and a bool-typed var.
	formulaBody := func(name string) string {
		return `formula = "` + name + `"
description = "beads-w5sk7 var-type-validation teeth"
version = 1
type = "workflow"

[vars.count]
description = "How many widgets"
type = "int"

[vars.enabled]
description = "Feature toggle"
type = "bool"

[[steps]]
id = "provision"
title = "Provision {{count}} widgets (enabled={{enabled}})"
description = "one step so the proto is valid"
`
	}

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

	t.Run("cook_runtime_rejects_non_int", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wti")
		path := writeFormula(t, dir, beadsDir, "wti-type")
		// count=foo is not a valid int; expect fail-loud rc!=0.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"cook", path, "--mode", "runtime",
			"--var", "count=foo", "--var", "enabled=true", "--json")
		if err == nil {
			t.Fatalf("beads-w5sk7: `cook --mode runtime` with count=foo (type:int, non-numeric) must "+
				"fail loud (rc!=0), got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "not a valid int") {
			t.Errorf("beads-w5sk7: int-type-violation error must mention \"not a valid int\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("cook_runtime_rejects_non_bool", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wtb")
		path := writeFormula(t, dir, beadsDir, "wtb-type")
		// enabled=maybe is not a valid bool; expect fail-loud.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"cook", path, "--mode", "runtime",
			"--var", "count=3", "--var", "enabled=maybe", "--json")
		if err == nil {
			t.Fatalf("beads-w5sk7: `cook --mode runtime` with enabled=maybe (type:bool) must "+
				"fail loud (rc!=0), got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "not a valid bool") {
			t.Errorf("beads-w5sk7: bool-type-violation error must mention \"not a valid bool\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("cook_runtime_accepts_valid_typed", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wtv")
		path := writeFormula(t, dir, beadsDir, "wtv-type")
		// Valid int + bool must still succeed (positive control: the fix rejects
		// ONLY type-invalid values, not everything). ParseBool accepts "1"/"0"
		// too, but "true"/"7" is the plain case.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"cook", path, "--mode", "runtime",
			"--var", "count=7", "--var", "enabled=false", "--json")
		if err != nil {
			t.Fatalf("beads-w5sk7: `cook --mode runtime` with valid count=7/enabled=false must "+
				"succeed; got err %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})

	t.Run("pour_rejects_non_int", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wtp")
		_ = writeFormula(t, dir, beadsDir, "wtp-type")
		// Pour cooks the formula inline (VarDefs populated), then would spawn
		// persistent issues. A non-int count must be rejected BEFORE spawn.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"mol", "pour", "wtp-type",
			"--var", "count=lots", "--var", "enabled=true", "--json")
		if err == nil {
			t.Fatalf("beads-w5sk7: `mol pour` with count=lots (type:int) must fail loud "+
				"(rc!=0) and NOT create persistent issues; got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "not a valid int") {
			t.Errorf("beads-w5sk7: pour int-type-violation error must mention \"not a valid int\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("pour_accepts_valid_typed", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wto")
		_ = writeFormula(t, dir, beadsDir, "wto-type")
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"mol", "pour", "wto-type",
			"--var", "count=42", "--var", "enabled=true", "--json")
		if err != nil {
			t.Fatalf("beads-w5sk7: `mol pour` with valid count=42/enabled=true must succeed; "+
				"got err %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})
}
