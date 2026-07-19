//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestFlagValidationErrorJSON_EmitsStdoutErrorObject is the end-to-end tooth for
// beads-3tgu (sibling of beads-71br). cobra & pflag validators that fire inside
// rootCmd.ExecuteC() BEFORE a command's RunE — required-flag, unknown-flag,
// flag-parse (invalid argument), and unknown-command (top-level typo AND
// leaf-command stray-arg) — never reach a per-command --json-aware handler.
// Previously each produced rc=1 with EMPTY stdout and a PLAINTEXT "Error: ..."
// on stderr, breaking any parser reading the stdout JSON contract. beads-71br
// fixed ONLY the arg-count leg; this covers the remaining pre-RunE classes via
// isCobraExecuteCValidationError + the position-independent wantsJSONOutput
// detector (some legs abort pflag parsing before a same-line --json is
// recorded, so intent is scanned from raw argv).
//
// Like the 71br tooth this cannot be a pure unit test: the defect lives in
// cobra's Execute plumbing, so the teeth run real bd as a subprocess and assert
// stdout is a parseable JSON error object.
func TestFlagValidationErrorJSON_EmitsStdoutErrorObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cases := []struct {
		name    string
		args    []string
		wantSub string // a stable substring expected in the JSON error message
	}{
		// required-flag: `bd gate create X` requires --blocks (cobra ValidateRequiredFlags).
		{"required_flag", []string{"gate", "create", "g1", "--json"}, "required flag(s)"},
		// unknown-flag, --json AFTER the bad flag (pflag aborts parsing at --bogusflag).
		{"unknown_flag_json_after", []string{"list", "--bogusflag", "--json"}, "unknown flag"},
		// unknown-flag, --json BEFORE the bad flag (position-independence).
		{"unknown_flag_json_before", []string{"list", "--json", "--bogusflag"}, "unknown flag"},
		// flag-parse: `--days notanint` fails pflag ParseInt.
		{"flag_parse", []string{"compact", "--days", "notanint", "--json"}, "invalid argument"},
		// unknown-command, top-level typo (cobra dispatch in ExecuteC).
		{"unknown_command_toplevel", []string{"frobnicate", "--json"}, "unknown command"},
		// unknown-command, top-level typo with --json FIRST.
		{"unknown_command_toplevel_json_first", []string{"--json", "frobnicate"}, "unknown command"},
		// unknown-command, leaf-command stray arg (cobra treats it as a subcommand).
		{"unknown_command_leaf_arg", []string{"stale", "bogus", "--json"}, "unknown command"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)

			// Must FAIL (non-zero) — a validation error is an error.
			if err == nil {
				t.Fatalf("`bd %s` unexpectedly succeeded (exit 0)\nstdout:\n%s",
					strings.Join(tc.args, " "), stdout.String())
			}

			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout is EMPTY on `bd %s` — the validation error must be a JSON object on stdout, not plaintext on stderr (beads-3tgu)\nstderr:\n%s",
					strings.Join(tc.args, " "), stderr.String())
			}

			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object on `bd %s`: %v\nstdout:\n%s\n(plaintext validation leak has regressed)",
					strings.Join(tc.args, " "), jerr, out)
			}

			// error message at top level or under a "data" envelope, matching the
			// canonical honored-json commands.
			msg, ok := obj["error"].(string)
			if !ok {
				if data, dok := obj["data"].(map[string]interface{}); dok {
					msg, ok = data["error"].(string)
				}
			}
			if !ok || msg == "" {
				t.Fatalf("expected a non-empty \"error\" field in the JSON object for `bd %s`, got: %s",
					strings.Join(tc.args, " "), out)
			}
			if !strings.Contains(msg, tc.wantSub) {
				t.Errorf("expected %q in the JSON error message for `bd %s`, got %q",
					tc.wantSub, strings.Join(tc.args, " "), msg)
			}
		})
	}
}

// TestFlagValidationError_NonJSONStaysPlaintext guards the regression boundary:
// WITHOUT --json, the same pre-RunE validation errors must stay plaintext on
// stderr with empty stdout (the fix must not blanket-JSON non-json invocations).
func TestFlagValidationError_NonJSONStaysPlaintext(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cases := [][]string{
		{"list", "--bogusflag"},
		{"compact", "--days", "notanint"},
		{"frobnicate"},
		{"stale", "bogus"},
	}

	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cmd := exec.Command(bd, args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)

			if err == nil {
				t.Fatalf("`bd %s` unexpectedly succeeded (exit 0)", strings.Join(args, " "))
			}
			if out := strings.TrimSpace(stdout.String()); out != "" {
				t.Errorf("non-json `bd %s` must not emit JSON on stdout, got: %s", strings.Join(args, " "), out)
			}
			if !strings.Contains(stderr.String(), "Error:") {
				t.Errorf("non-json `bd %s` expected plaintext 'Error:' on stderr, got: %s", strings.Join(args, " "), stderr.String())
			}
		})
	}
}

// TestUnknownSubcommandGuardStillJSON guards the dthi boundary: a pure
// parent-group unknown-SUBCOMMAND (e.g. `bd label bogussub`) is already
// JSON-correct via the subcmd_guard (attachUnknownSubcommandGuards →
// HandleErrorRespectJSON), NOT via the ExecuteC central handler. This asserts
// the 3tgu change did not disturb it (distinct guard path, distinct message).
func TestUnknownSubcommandGuardStillJSON(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "label", "bogussub", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("`bd label bogussub --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}
	out := strings.TrimSpace(stdout.String())
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("`bd label bogussub --json` stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", jerr, out, stderr.String())
	}
	msg, _ := obj["error"].(string)
	if data, ok := obj["data"].(map[string]interface{}); ok && msg == "" {
		msg, _ = data["error"].(string)
	}
	if !strings.Contains(msg, "subcommand") {
		t.Errorf("expected the parent-group subcommand-guard message, got %q", msg)
	}
}
