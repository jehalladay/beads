//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestReadyProxiedMetadataJSONError_EmitsStdoutObject is the proxied-mode teeth
// for beads-7cnyb: the three metadata validation legs in gatherReadyInput
// (ready_input.go), reached ONLY via the proxied ready path
// (ready_proxied_server.go → gatherReadyInput), used bare
// fmt.Fprintf(os.Stderr, ...) + os.Exit(1) → under --json the error went to
// STDERR as plaintext with an EMPTY stdout, breaking parsers. The fix swaps
// them to FatalErrorRespectJSON (stdout {error}), matching the direct
// ready.go:299/302/310 HandleErrorRespectJSON twin and the ~19 sibling legs in
// the same function (4 of which beads-ag3ru fixed).
//
// Proxied-server mode is enterable (beads-iu9f un-gated bd init
// --proxied-server), so these legs are live, not dead defensive code. Each
// subtest RED before the fix (empty stdout, plaintext on stderr), GREEN after
// (non-empty stdout that json.Unmarshals to {error}).
func TestReadyProxiedMetadataJSONError_EmitsStdoutObject(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "rcm")

	cases := []struct {
		name       string
		args       []string
		wantSubstr string
	}{
		// value without "=" → not key=value (ready_input.go:321 leg).
		{"metadata_field_no_eq", []string{"ready", "--metadata-field=nokey", "--json"}, "metadata-field"},
		// key starts with a digit → fails ValidateMetadataKey (:328 leg).
		{"metadata_field_bad_key", []string{"ready", "--metadata-field=1bad=v", "--json"}, "metadata-field key"},
		// invalid has-metadata-key (:335 leg).
		{"has_metadata_key_bad", []string{"ready", "--has-metadata-key=1bad", "--json"}, "has-metadata-key"},
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
				t.Fatalf("stdout is EMPTY for `bd %s` — the proxied ready metadata validation error must emit a JSON {error} object on stdout (json-error-contract beads-7cnyb)\nstderr:\n%s",
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
