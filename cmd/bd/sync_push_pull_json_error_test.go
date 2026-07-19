//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// beads-nqv0: the shared push/pull handlers in sync_push_pull.go (all 6
// trackers) and `notion sync` returned their config/setup/validation errors via
// a bare `fmt.Errorf` or plain `HandleError`, so under `--json` cobra printed a
// plaintext "Error: ..." line to stderr and exited 1 with EMPTY stdout —
// unparseable by a --json consumer — while the success path (outputSyncResult /
// writeNotionJSON) emits JSON. The fix routes the reachable-under-json error
// paths through HandleErrorRespectJSON (8lqh error-contract, ERROR half;
// distinct from the lav0 success-half).
//
// The earliest reachable guard on each push handler is the missing-args check
// ("at least one bead ID is required"), which fires BEFORE any store/env/network
// use — so these assertions are fully hermetic (no live Dolt server required).
func TestSyncPushJSONErrorContract_nqv0(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*cobra.Command, []string) error
		cmd  *cobra.Command
	}{
		{"ado_push", runADOPush, adoPushCmd},
		{"jira_push", runJiraPush, jiraPushCmd},
		{"linear_push", runLinearPush, linearPushCmd},
		{"github_push", runGitHubPush, githubPushCmd},
		{"gitlab_push", runGitLabPush, gitlabPushCmd},
		{"notion_push", runNotionPush, notionPushCmd},
	}

	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := captureStdoutExpectErr(t, func() error {
				return tc.fn(tc.cmd, nil) // no args → "at least one bead ID is required"
			})
			if err == nil {
				t.Fatalf("%s: expected a non-nil error with no args, got nil (stdout=%q)", tc.name, out)
			}
			assertSyncJSONError(t, tc.name, out)
		})
	}
}

// assertSyncJSONError asserts stdout is a JSON object carrying an "error" field
// — the shape HandleErrorRespectJSON emits under --json.
func assertSyncJSONError(t *testing.T, label, stdout string) {
	t.Helper()
	s := strings.TrimSpace(stdout)
	if s == "" {
		t.Fatalf("%s: stdout empty on a --json push error — must emit a JSON error object (beads-nqv0)", label)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("%s: stdout is not a JSON object on --json error: %v\nstdout:\n%s", label, jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("%s: expected an \"error\" field in the --json stdout object, got: %s", label, s)
	}
}
