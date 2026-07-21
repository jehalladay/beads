//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestReadyProxiedJSONError_EmitsStdoutObject is the proxied-mode teeth for
// beads-ag3ru: the four validation legs in gatherReadyInput (ready_input.go),
// reached ONLY via the proxied ready path (ready_proxied_server.go →
// gatherReadyInput), used bare FatalError → under --json the {error} object
// went to STDERR with an EMPTY stdout, breaking parsers. The fix swaps them to
// FatalErrorRespectJSON (stdout {error}), matching the ~15 sibling legs in the
// same function and the direct ready.go path.
//
// Proxied-server mode is now enterable (beads-iu9f un-gated bd init
// --proxied-server), so gatherReadyInput's validation legs are live, not dead
// defensive code. Each subtest RED before the fix (empty stdout, JSON on
// stderr), GREEN after (non-empty stdout that json.Unmarshals to {error}).
func TestReadyProxiedJSONError_EmitsStdoutObject(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "rag")

	cases := []struct {
		name       string
		args       []string
		wantSubstr string
	}{
		{"limit_negative", []string{"ready", "--limit=-1", "--json"}, "limit"},
		{"offset_negative", []string{"ready", "--offset=-1", "--json"}, "offset"},
		{"mol_type_invalid", []string{"ready", "--mol-type=bogus", "--json"}, "mol-type"},
		{"sort_invalid", []string{"ready", "--sort=bogus", "--json"}, "sort policy"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, tc.args...)
			if err == nil {
				t.Fatalf("expected non-zero exit for `bd %s`, got success\nstdout:\n%s\nstderr:\n%s",
					strings.Join(tc.args, " "), stdout, stderr)
			}

			out := strings.TrimSpace(stdout)
			if out == "" {
				t.Fatalf("stdout is EMPTY for `bd %s` — the proxied ready validation error must emit a JSON {error} object on stdout (json-error-contract beads-ag3ru)\nstderr:\n%s",
					strings.Join(tc.args, " "), stderr)
			}

			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object for `bd %s`: %v\nstdout:\n%s",
					strings.Join(tc.args, " "), jerr, out)
			}
			msg, ok := obj["error"]
			if !ok {
				t.Fatalf("expected an \"error\" field in stdout JSON for `bd %s`, got: %s",
					strings.Join(tc.args, " "), out)
			}
			if s, _ := msg.(string); !strings.Contains(s, tc.wantSubstr) {
				t.Errorf("expected the error to mention %q for `bd %s`, got %q",
					tc.wantSubstr, strings.Join(tc.args, " "), s)
			}
		})
	}
}
