//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestQuerySearchMissingArgJSONError_h2d5 is the error-contract teeth for
// beads-h2d5. `bd query` and `bd search` treat a missing query expression as an
// ERROR (rc1), but on that error path they called cmd.Help() — which dumps the
// full help text to STDOUT — breaking the --json contract:
//
//   - query.go: len(args)==0 → Fprintf(stderr) + cmd.Help() [STDOUT] + SilentExit()
//     → NO json object ever emitted, and help pollutes stdout.
//   - search.go parseSearchQuery: query=="" → cmd.Help() [STDOUT] THEN
//     HandleErrorRespectJSON(...) → a valid json error doc PRECEDED by help text
//     on stdout = double-content, invalid json.
//
// The fix gates cmd.Help() behind `if !jsonOutput` at all sites and, for query,
// emits the error via HandleErrorRespectJSON instead of a silent exit. Under
// --json the failing command must produce a SINGLE parseable JSON {error} object
// on stdout with no help text. (8lqh --json-error-contract class; distinct from
// 71br which was ExecuteC pre-RunE cobra arg-count validators.)
func TestQuerySearchMissingArgJSONError_h2d5(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cases := []struct {
		name string
		args []string
	}{
		{"query_missing_expression", []string{"query", "--json"}},
		{"search_missing_query", []string{"search", "--json"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err == nil {
				t.Fatalf("`bd %s` unexpectedly succeeded\nstdout:\n%s", strings.Join(tc.args, " "), stdout.String())
			}

			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout is EMPTY on a failing `bd %s` — under --json the error must be a JSON object on stdout (beads-h2d5)\nstderr:\n%s", strings.Join(tc.args, " "), stderr.String())
			}

			// The whole of stdout must be a single JSON object. Help-text
			// pollution (a usage banner before/after the json doc) makes the
			// full stdout unparseable — that is the exact defect.
			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a SINGLE JSON object on a failing `bd %s` (help-text pollution breaks --json, beads-h2d5): %v\nstdout:\n%s", strings.Join(tc.args, " "), jerr, out)
			}
			msg, ok := obj["error"].(string)
			if !ok {
				if data, dok := obj["data"].(map[string]interface{}); dok {
					msg, ok = data["error"].(string)
				}
			}
			if !ok || msg == "" {
				t.Errorf("expected a non-empty \"error\" field in failing `bd %s` stdout, got: %s", strings.Join(tc.args, " "), out)
			}

			// The help banner (a distinctive "Usage:" line) must not appear on
			// stdout under --json.
			if strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("`bd %s` leaked help text (\"Usage:\") to stdout under --json (beads-h2d5)\nstdout:\n%s", strings.Join(tc.args, " "), stdout.String())
			}
		})
	}
}

// TestQueryMissingArgJSONError_h2d5_Proxied covers the proxied-server query path
// (query_proxied_server.go), which had the same cmd.Help()-to-stdout defect as
// the direct path. Runs only under BEADS_TEST_PROXIED_SERVER=1.
func TestQueryMissingArgJSONError_h2d5_Proxied(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "qh")

	stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "query", "--json")
	if err == nil {
		t.Fatalf("`bd query --json` (no expression) unexpectedly succeeded in proxied mode; stdout:\n%s", stdout)
	}
	out := strings.TrimSpace(stdout)
	if out == "" {
		t.Fatalf("proxied `bd query --json` (no expression) emitted empty stdout — must be a JSON {error} object (beads-h2d5); stderr:\n%s", stderr)
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("proxied `bd query --json` failure stdout is not a SINGLE JSON object (help pollution): %v\nstdout:\n%s", jerr, out)
	}
	if msg, ok := obj["error"].(string); !ok || msg == "" {
		t.Errorf("proxied `bd query --json` failure stdout has no non-empty \"error\" field: %s", out)
	}
	if strings.Contains(stdout, "Usage:") {
		t.Errorf("proxied `bd query --json` leaked help text (\"Usage:\") to stdout (beads-h2d5)\nstdout:\n%s", stdout)
	}
}
