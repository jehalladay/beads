//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestDeferJSONValidationErrors_v02z is the error-contract teeth for beads-v02z.
// `bd defer` honors the persistent --json on its success path (outputJSON with the
// deferred issues) and already routes its ID-resolution error through
// HandleErrorRespectJSON (defer.go:74, beads-0l4c) — but two upstream validation
// guards used a plain HandleError: an invalid --until format (defer.go:52) and an
// empty --reason (defer.go:64). Under `bd defer <id> ... --json` those produced
// EMPTY stdout + stderr text, unparseable by a --json consumer. The fix routes
// both through HandleErrorRespectJSON (0wp9/21xi/yw6g --json-error-contract class).
//
// Both guards fire BEFORE any ID resolution or store access, so they are
// deterministic, server-free errors reachable with any arg — the teeth run bd as
// a subprocess and assert stdout is a parseable JSON object with an "error" field.
func TestDeferJSONValidationErrors_v02z(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cases := []struct {
		name string
		args []string
	}{
		{"invalid_until", []string{"defer", "bd-nonexistent", "--until=garbage", "--json"}},
		{"empty_reason", []string{"defer", "bd-nonexistent", "--reason=", "--json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			// Expected to FAIL (invalid validation) — err != nil is fine.
			if err == nil {
				t.Fatalf("`bd %s` unexpectedly succeeded\nstdout:\n%s", strings.Join(tc.args, " "), stdout.String())
			}

			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout is EMPTY on a failing `bd %s` — the error must be emitted as a JSON object on stdout (plain-text HandleError breaks parsers, beads-v02z)\nstderr:\n%s", strings.Join(tc.args, " "), stderr.String())
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
