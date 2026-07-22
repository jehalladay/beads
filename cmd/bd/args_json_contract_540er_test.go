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

// beads-540er: the custom cobra Args: validators for `bd dep add` and `bd list`
// returned bare fmt.Errorf for flag-combo / stray-positional rejections. Those
// validators fire inside rootCmd.ExecuteC() before RunE, so they never reach a
// --json-aware handler; and main.go's central rescue only recognizes arg-count
// (isArgCountError) + cobra/pflag literals (isCobraExecuteCValidationError).
// The custom messages match NEITHER, so under --json they fell through to the
// SilenceErrors plaintext-stderr branch with empty stdout — breaking the JSON
// contract (same class as beads-71br/3tgu, generalizing beads-t1lx maintNoArgs).
// The fix routes them through argValidationError, which honors --json.

// argValidationError must preserve the plaintext error verbatim when --json is
// NOT set (no behavior change for interactive users).
func TestArgValidationErrorPlaintextWhenNotJSON_540er(t *testing.T) {
	cmd := &cobra.Command{Use: "add"}
	cmd.Flags().Bool("json", false, "")

	err := argValidationError(cmd, "--file cannot be used with positional issue IDs")
	if err == nil {
		t.Fatal("argValidationError: expected an error, got nil")
	}
	if got := err.Error(); got != "--file cannot be used with positional issue IDs" {
		t.Fatalf("non-json error text must be preserved verbatim, got %q", got)
	}
	// It must NOT be an *exitError in the plaintext path — main prints it via
	// the SilenceErrors branch exactly as before.
	if _, ok := exitCodeFromError(err); ok {
		t.Errorf("non-json path must return a plain error (not an *exitError), got %v", err)
	}
}

// argValidationError under --json must emit a parseable JSON error object on
// stdout and return an *exitError (consumed by exitCodeFromError before main's
// plaintext-stderr print), so no plaintext leaks.
func TestArgValidationErrorJSONContract_540er(t *testing.T) {
	cmd := &cobra.Command{Use: "add"}
	cmd.Flags().Bool("json", true, "")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := argValidationError(cmd, "cannot use both positional depends-on-id and --blocked-by/--depends-on flag")

	_ = w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if err == nil {
		t.Fatal("argValidationError under --json: expected an error, got nil")
	}
	if _, ok := exitCodeFromError(err); !ok {
		t.Errorf("under --json the validator must return an *exitError (consumed before the plaintext stderr print), got %v", err)
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal(out, &obj); jerr != nil {
		t.Fatalf("under --json stdout must be a parseable JSON error object, got %q (unmarshal: %v)", string(out), jerr)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("JSON error object should carry an 'error' field, got %v", obj)
	}
}

// Wire test: `bd dep add`'s Args validator must route ALL its custom flag-combo
// rejections through the --json-aware path. Exercises the exact reachable
// invocations (--file + positional, --file + flag, both positional + flag).
func TestDepAddArgsValidatorJSONContract_540er(t *testing.T) {
	cases := []struct {
		name  string
		flags map[string]string
		args  []string
	}{
		{"file_with_positional", map[string]string{"file": "x.jsonl"}, []string{"bd-1"}},
		{"file_with_flag", map[string]string{"file": "x.jsonl", "blocked-by": "bd-2"}, []string{}},
		{"positional_and_flag", map[string]string{"blocked-by": "bd-2"}, []string{"bd-1", "bd-3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh command instance so flag state doesn't bleed across cases.
			cmd := &cobra.Command{Use: "add"}
			cmd.Flags().Bool("json", true, "")
			cmd.Flags().String("file", "", "")
			cmd.Flags().String("blocked-by", "", "")
			cmd.Flags().String("depends-on", "", "")
			for k, v := range tc.flags {
				_ = cmd.Flags().Set(k, v)
			}

			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err := depAddCmd.Args(cmd, tc.args)

			_ = w.Close()
			os.Stdout = oldStdout
			out, _ := io.ReadAll(r)

			if err == nil {
				t.Fatalf("%s: expected a validation error, got nil", tc.name)
			}
			if _, ok := exitCodeFromError(err); !ok {
				t.Fatalf("%s: under --json the custom rejection must be an *exitError (no plaintext leak), got %v", tc.name, err)
			}
			var obj map[string]interface{}
			if jerr := json.Unmarshal(out, &obj); jerr != nil {
				t.Fatalf("%s: under --json stdout must be a parseable JSON error, got %q (%v)", tc.name, string(out), jerr)
			}
		})
	}
}

// The arg-count leg of `bd dep add` (flag provided, zero positionals) stays a
// plaintext arg-count error — it is intentionally rescued centrally by
// isArgCountError in main.go, so it must NOT be converted here (regression
// guard against over-applying the helper).
func TestDepAddArgsCountLegStaysArgCountError_540er(t *testing.T) {
	cmd := &cobra.Command{Use: "add"}
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().String("file", "", "")
	cmd.Flags().String("blocked-by", "", "")
	cmd.Flags().String("depends-on", "", "")
	_ = cmd.Flags().Set("blocked-by", "bd-2")

	err := depAddCmd.Args(cmd, []string{}) // flag set, 0 positionals => arg-count leg
	if err == nil {
		t.Fatal("expected an arg-count error, got nil")
	}
	if !isArgCountError(err) {
		t.Fatalf("the flag+zero-positional leg must remain an isArgCountError (centrally rescued), got %q", err.Error())
	}
	// It must NOT be pre-converted to an *exitError — the central handler owns it.
	if _, ok := exitCodeFromError(err); ok {
		t.Errorf("arg-count leg must stay a plain error for the central rescue, got an *exitError")
	}
}

// Wire test: `bd list`'s Args validator routes its stray-positional and
// known-flag-as-positional rejections through the --json-aware path.
func TestListArgsValidatorJSONContract_540er(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"stray_positional", []string{"bogus"}},
		{"known_flag_as_positional", []string{"ready"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "list"}
			cmd.Flags().Bool("json", true, "")

			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err := listCmd.Args(cmd, tc.args)

			_ = w.Close()
			os.Stdout = oldStdout
			out, _ := io.ReadAll(r)

			if err == nil {
				t.Fatalf("%s: expected a validation error, got nil", tc.name)
			}
			if _, ok := exitCodeFromError(err); !ok {
				t.Fatalf("%s: under --json the rejection must be an *exitError (no plaintext leak), got %v", tc.name, err)
			}
			var obj map[string]interface{}
			if jerr := json.Unmarshal(out, &obj); jerr != nil {
				t.Fatalf("%s: under --json stdout must be a parseable JSON error, got %q (%v)", tc.name, string(out), jerr)
			}
		})
	}
}

// Parity negative: with --json OFF, bd list's rejection stays plaintext (so
// interactive users still see the helpful "did you mean" hint verbatim).
func TestListArgsValidatorPlaintextWhenNotJSON_540er(t *testing.T) {
	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().Bool("json", false, "")

	err := listCmd.Args(cmd, []string{"ready"})
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("non-json list rejection should keep the 'did you mean' hint, got %q", err.Error())
	}
	if _, ok := exitCodeFromError(err); ok {
		t.Errorf("non-json path must return a plain error, got an *exitError")
	}
}
