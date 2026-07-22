//go:build cgo

package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// beads-1xji0: follow-up sweep to beads-540er. Three more custom cobra Args:
// validators returned bare fmt.Errorf for stray-positional rejections:
//   - count.go        countArgs        (2 messages)
//   - compact.go      compactNoArgs    (1 message)
//   - compact_dolt.go compactDoltNoArgs(1 message)
// They fire inside rootCmd.ExecuteC() before RunE, so they never reach a
// --json-aware handler; main.go's central rescue only recognizes arg-count
// (isArgCountError) + cobra/pflag literals (isCobraExecuteCValidationError).
// The custom messages match NEITHER, so under --json they fell through to the
// SilenceErrors plaintext-stderr branch with empty stdout — the same JSON
// contract break beads-540er fixed for dep add / list. The fix routes them
// through argValidationError, which honors --json. These teeth exercise the
// real validators (not a stand-in) so a future edit that drops the helper
// re-fails here.

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what
// was written (mirrors the os.Pipe pattern in args_json_contract_540er_test.go).
func captureStdout1xji0(t *testing.T, fn func()) []byte {
	t.Helper()
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)
	return out
}

// assertJSONExitError asserts the validator returned an *exitError (consumed by
// exitCodeFromError before main's plaintext-stderr print) AND emitted a
// parseable JSON error object on stdout — i.e. no plaintext leak under --json.
func assertJSONExitError1xji0(t *testing.T, err error, out []byte) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	if _, ok := exitCodeFromError(err); !ok {
		t.Fatalf("under --json the custom rejection must be an *exitError (no plaintext leak), got %v", err)
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal(out, &obj); jerr != nil {
		t.Fatalf("under --json stdout must be a parseable JSON error object, got %q (%v)", string(out), jerr)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("JSON error object should carry an 'error' field, got %v", obj)
	}
}

// bd count's countArgs must route BOTH its rejection messages (the key=value
// "did you mean --X" hint leg and the generic stray-positional leg) through the
// --json-aware path.
func TestCountArgsValidatorJSONContract_1xji0(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"keyvalue_hint_leg", []string{"status=open"}}, // status is a real list/count flag → hint leg
		{"generic_stray_leg", []string{"bogus"}},       // not key=value → generic leg
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "count"}
			cmd.Flags().Bool("json", true, "")
			cmd.Flags().String("status", "", "") // so the key=value leg finds a matching flag

			var err error
			out := captureStdout1xji0(t, func() { err = countArgs(cmd, tc.args) })
			assertJSONExitError1xji0(t, err, out)
		})
	}
}

// With --json OFF, bd count's rejection stays plaintext (interactive users keep
// the verbatim hint) and is NOT an *exitError.
func TestCountArgsValidatorPlaintextWhenNotJSON_1xji0(t *testing.T) {
	cmd := &cobra.Command{Use: "count"}
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().String("status", "", "")

	err := countArgs(cmd, []string{"status=open"})
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	if !strings.Contains(err.Error(), "did you mean --status") {
		t.Errorf("non-json count rejection should keep the 'did you mean --status' hint, got %q", err.Error())
	}
	if _, ok := exitCodeFromError(err); ok {
		t.Errorf("non-json path must return a plain error (not an *exitError), got %v", err)
	}
}

// bd compact's compactNoArgs must route its stray-positional rejection through
// the --json-aware path.
func TestCompactNoArgsValidatorJSONContract_1xji0(t *testing.T) {
	cmd := &cobra.Command{Use: "compact"}
	cmd.Flags().Bool("json", true, "")

	var err error
	out := captureStdout1xji0(t, func() { err = compactNoArgs(cmd, []string{"bd-42"}) })
	assertJSONExitError1xji0(t, err, out)
}

// compact plaintext parity: with --json OFF the rejection stays plaintext + not
// an *exitError, and keeps the --id hint.
func TestCompactNoArgsValidatorPlaintextWhenNotJSON_1xji0(t *testing.T) {
	cmd := &cobra.Command{Use: "compact"}
	cmd.Flags().Bool("json", false, "")

	err := compactNoArgs(cmd, []string{"bd-42"})
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	if !strings.Contains(err.Error(), "--id") {
		t.Errorf("non-json compact rejection should keep the --id hint, got %q", err.Error())
	}
	if _, ok := exitCodeFromError(err); ok {
		t.Errorf("non-json path must return a plain error (not an *exitError), got %v", err)
	}
}

// bd compact (Dolt history squash) compactDoltNoArgs must route its
// stray-positional rejection through the --json-aware path.
func TestCompactDoltNoArgsValidatorJSONContract_1xji0(t *testing.T) {
	cmd := &cobra.Command{Use: "compact"}
	cmd.Flags().Bool("json", true, "")

	var err error
	out := captureStdout1xji0(t, func() { err = compactDoltNoArgs(cmd, []string{"somebead"}) })
	assertJSONExitError1xji0(t, err, out)
}

// compact_dolt plaintext parity.
func TestCompactDoltNoArgsValidatorPlaintextWhenNotJSON_1xji0(t *testing.T) {
	cmd := &cobra.Command{Use: "compact"}
	cmd.Flags().Bool("json", false, "")

	err := compactDoltNoArgs(cmd, []string{"somebead"})
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	if !strings.Contains(err.Error(), "--days/--force") {
		t.Errorf("non-json compact(dolt) rejection should keep the flag hint, got %q", err.Error())
	}
	if _, ok := exitCodeFromError(err); ok {
		t.Errorf("non-json path must return a plain error (not an *exitError), got %v", err)
	}
}

// Guard: the no-positional happy path stays a nil error for all three (a stray
// over-application of the helper must not reject valid flag-only invocations).
func TestArgsValidatorsAcceptZeroPositionals_1xji0(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().Bool("json", true, "")
	if err := countArgs(cmd, []string{}); err != nil {
		t.Errorf("countArgs([]) must be nil, got %v", err)
	}
	if err := compactNoArgs(cmd, []string{}); err != nil {
		t.Errorf("compactNoArgs([]) must be nil, got %v", err)
	}
	if err := compactDoltNoArgs(cmd, []string{}); err != nil {
		t.Errorf("compactDoltNoArgs([]) must be nil, got %v", err)
	}
}
