//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWispVarValidation_d9d8y is the beads-d9d8y teeth: `bd mol wisp <proto>`
// materializes an ephemeral molecule DAG from a cooked formula, substituting
// --var values into real (ephemeral) issue content, but — unlike `bd cook
// --mode runtime` and `bd mol pour` (fixed by beads-8m9o7) — it never called
// formula.ValidateVarValues, so out-of-enum / pattern-violating var values were
// accepted silently and baked into the wisp's issues. This is the wisp sibling
// of the 8m9o7 cook/pour var-validation gap.
//
// The fix wires formula.ValidateVarValues into runWispCreateCore (wisp.go),
// after the missing-required check and before the dry-run/spawn, failing loud
// (rc=1, JSON-aware) at parity with pour. These teeth drive the real embedded
// `bd` subprocess through the CLI so the enforcement is verified at the
// reachable command layer (a pure ValidateVarValues unit test false-greens on
// the missing wiring — the whole defect is that wisp did not CALL it).
//
// Mutation-verify: remove the ValidateVarValues call from runWispCreateCore and
// the reject subtests go RED (invalid values succeed with rc=0).
func TestWispVarValidation_d9d8y(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A formula with an enum-constrained var and a pattern-constrained var.
	formulaBody := func(name string) string {
		return `formula = "` + name + `"
description = "beads-d9d8y wisp var-validation teeth"
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

	// writeFormula drops the formula into the <beadsDir>/formulas registry so
	// `bd mol wisp <name>` resolves it by name (parser.LoadByName), matching the
	// dogfooder repro `bd mol wisp mol-enum --var ...`.
	writeFormula := func(t *testing.T, beadsDir, name string) {
		t.Helper()
		regDir := filepath.Join(beadsDir, "formulas")
		if err := os.MkdirAll(regDir, 0o755); err != nil {
			t.Fatalf("mkdir formulas registry: %v", err)
		}
		if err := os.WriteFile(filepath.Join(regDir, name+".formula.toml"), []byte(formulaBody(name)), 0o644); err != nil {
			t.Fatalf("write registry formula %s: %v", name, err)
		}
	}

	t.Run("wisp_rejects_out_of_enum", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wve")
		writeFormula(t, beadsDir, "wve-enum")
		// platinum is NOT in enum=[gold,silver]; must fail loud BEFORE spawning
		// the ephemeral wisp DAG.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"mol", "wisp", "wve-enum",
			"--var", "tier=platinum", "--var", "code=ABC", "--json")
		if err == nil {
			t.Fatalf("beads-d9d8y: `mol wisp` with tier=platinum (out of enum) must fail loud "+
				"(rc!=0) and NOT create wisp issues; got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "not in allowed values") {
			t.Errorf("beads-d9d8y: wisp enum-violation error must mention \"not in allowed values\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("wisp_rejects_pattern_violation", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wvp")
		writeFormula(t, beadsDir, "wvp-enum")
		// ab1 fails pattern ^[A-Z]{3}$; expect fail-loud.
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"mol", "wisp", "wvp-enum",
			"--var", "tier=gold", "--var", "code=ab1", "--json")
		if err == nil {
			t.Fatalf("beads-d9d8y: `mol wisp` with code=ab1 (pattern violation) must fail loud "+
				"(rc!=0), got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "does not match pattern") {
			t.Errorf("beads-d9d8y: wisp pattern-violation error must mention \"does not match pattern\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("wisp_dryrun_rejects_out_of_enum", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wvd")
		writeFormula(t, beadsDir, "wvd-enum")
		// The validation runs BEFORE the dry-run block, so --dry-run also rejects
		// a bad value (a --dry-run preview of an invalid wisp is misleading).
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"mol", "wisp", "wvd-enum", "--dry-run",
			"--var", "tier=platinum", "--var", "code=ABC", "--json")
		if err == nil {
			t.Fatalf("beads-d9d8y: `mol wisp --dry-run` with tier=platinum (out of enum) must fail "+
				"loud (rc!=0), got rc=0.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "not in allowed values") {
			t.Errorf("beads-d9d8y: wisp --dry-run enum-violation error must mention \"not in allowed values\"; "+
				"got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("wisp_accepts_valid", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wvv")
		writeFormula(t, beadsDir, "wvv-enum")
		// Valid values must still succeed (positive control: the fix rejects ONLY
		// invalid values, not everything).
		stdout, stderr, err := bdRun8m9o7(t, bd, dir,
			"mol", "wisp", "wvv-enum",
			"--var", "tier=silver", "--var", "code=XYZ", "--json")
		if err != nil {
			t.Fatalf("beads-d9d8y: `mol wisp` with valid tier=silver/code=XYZ must succeed; "+
				"got err %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})
}
