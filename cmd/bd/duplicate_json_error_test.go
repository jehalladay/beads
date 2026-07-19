//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestDuplicateSupersedeJSONError_dd43 is the error-contract teeth for beads-dd43.
// `bd duplicate` and `bd supersede` honor the persistent --json on their success
// path (outputJSON with the closed-issue result) but every error return used a
// plain fmt.Errorf. Under `bd duplicate <id> --of <x> --json` those produced EMPTY
// stdout + stderr text, unparseable by a --json consumer. The fix routes each error
// through HandleErrorRespectJSON (8lqh EMPTY-stdout --json-error-contract class;
// v02z/0wp9 precedent).
//
// The ID-resolution error fires before any write and is deterministic with a
// nonexistent id, so the teeth run bd as a subprocess and assert stdout is a
// parseable JSON object with an "error" field.
func TestDuplicateSupersedeJSONError_dd43(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cases := []struct {
		name string
		args []string
	}{
		{"duplicate_unresolvable", []string{"duplicate", "bd-nonexistent", "--of", "bd-alsomissing", "--json"}},
		{"supersede_unresolvable", []string{"supersede", "bd-nonexistent", "--with", "bd-alsomissing", "--json"}},
	}

	for _, tc := range cases {
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
				t.Fatalf("stdout is EMPTY on a failing `bd %s` — the error must be emitted as a JSON object on stdout (plain-text fmt.Errorf breaks parsers, beads-dd43)\nstderr:\n%s", strings.Join(tc.args, " "), stderr.String())
			}

			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object on a failing `bd %s`: %v\nstdout:\n%s", strings.Join(tc.args, " "), jerr, out)
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
		})
	}
}
