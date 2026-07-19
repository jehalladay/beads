//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestArgCountErrorJSON_EmitsStdoutErrorObject is the end-to-end tooth for
// beads-71br. cobra positional-arg-count validators (cobra.ExactArgs/
// MinimumNArgs/MaximumNArgs/RangeArgs) fire inside rootCmd.ExecuteC() BEFORE a
// command's RunE, so an arg-count failure (e.g. `bd diff a` needs 2 args)
// never reaches a --json-aware handler: it previously produced rc=1 with EMPTY
// stdout and a PLAINTEXT "Error: accepts 2 arg(s), received 1" on stderr,
// breaking any parser reading the stdout JSON contract. The fix detects an
// arg-count error in the main.go ExecuteC error branch and, when --json is set
// on the executed command, emits {error,schema_version} on stdout (matching
// HandleErrorRespectJSON) instead of the plaintext-stderr path.
//
// This cannot be a pure unit test: the defect lives in cobra's Execute plumbing
// (the Args validator runs before RunE), so the teeth run real bd as a
// subprocess and assert stdout is a parseable JSON error object.
func TestArgCountErrorJSON_EmitsStdoutErrorObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Each case invokes a leaf command with the WRONG positional arg count while
	// --json is set. All use a cobra Args: validator (diff=ExactArgs(2),
	// link=ExactArgs(2), supersede=ExactArgs(1)), so the error fires pre-RunE.
	cases := []struct {
		name string
		args []string
	}{
		{"diff_one_arg", []string{"diff", "a", "--json"}},
		{"link_one_arg", []string{"link", "x", "--json"}},
		{"supersede_zero_args", []string{"supersede", "--json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)

			// Must FAIL (non-zero) — wrong arg count is an error.
			if err == nil {
				t.Fatalf("`bd %s` unexpectedly succeeded (exit 0)\nstdout:\n%s",
					strings.Join(tc.args, " "), stdout.String())
			}

			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout is EMPTY on `bd %s --json` — the arg-count error must be a JSON object on stdout, not plaintext on stderr (beads-71br)\nstderr:\n%s",
					strings.Join(tc.args, " "), stderr.String())
			}

			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object on `bd %s`: %v\nstdout:\n%s\n(plaintext arg-count leak has regressed)",
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
			if !strings.Contains(msg, "arg(s)") {
				t.Errorf("expected an arg-count error message for `bd %s`, got %q", strings.Join(tc.args, " "), msg)
			}
		})
	}
}
