//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestLabelReopenJSONStderrContract_en28 is the error-contract teeth for
// beads-en28. The DIRECT (non-proxied) paths of `bd label add`, `bd label
// remove`, and `bd reopen` emitted an UNGATED plain-text "Error resolving
// <id>: ..." line to STDERR under --json (label.go / reopen.go resolve loops).
// On a wholly-failed batch the terminal error object goes to stdout as JSON
// (HandleErrorRespectJSON), so a `--json 2>&1` consumer got a MIXED stream
// (plain text + JSON) and json.load FAILED — the same fg6/92tz clean-stderr
// contract show.go already models with a deferred reportShowItemError.
//
// The fix defers those per-item messages under --json: on a wholly-failed
// batch the single stdout JSON object is the sole error and stderr stays clean;
// on partial success they flush to stderr as JSON objects. These teeth run bd
// as a subprocess against a real embedded DB and assert the COMBINED (2>&1)
// stream on a wholly-failed batch is exactly one parseable JSON object.
func TestLabelReopenJSONStderrContract_en28(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Every command below targets a non-existent id so the DIRECT resolve loop
	// fails for every arg (wholly-failed batch) — the exact repro shape.
	cases := []struct {
		name string
		args []string
	}{
		{"label_add", []string{"label", "add", "zz-999", "somelabel", "--json"}},
		{"label_remove", []string{"label", "remove", "zz-999", "somelabel", "--json"}},
		{"reopen", []string{"reopen", "zz-999", "--json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// COMBINED stream: stderr redirected into stdout, exactly what a
			// `bd ... --json 2>&1 | json.load` pipeline sees.
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			combined, cerr := cmd.CombinedOutput()
			// Expected to FAIL (unresolvable id) — a non-nil exit is fine.
			if cerr == nil {
				t.Fatalf("`bd %s` unexpectedly succeeded\ncombined:\n%s", strings.Join(tc.args, " "), combined)
			}

			out := strings.TrimSpace(string(combined))
			if out == "" {
				t.Fatalf("combined 2>&1 stream is EMPTY on a failing `bd %s` — expected exactly one JSON error object", strings.Join(tc.args, " "))
			}

			// The whole 2>&1 stream must be a single parseable JSON object; an
			// interleaved plain-text "Error resolving ..." line breaks this
			// (the beads-en28 symptom).
			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("combined 2>&1 stream is NOT a single JSON object on a failing `bd %s` (interleaved plain text breaks --json consumers, beads-en28): %v\ncombined:\n%s",
					strings.Join(tc.args, " "), jerr, out)
			}
			msg, ok := obj["error"].(string)
			if !ok {
				if data, dok := obj["data"].(map[string]interface{}); dok {
					msg, ok = data["error"].(string)
				}
			}
			if !ok || msg == "" {
				t.Errorf("expected a non-empty \"error\" field in failing `bd %s` output, got: %s", strings.Join(tc.args, " "), out)
			}
		})
	}
}
