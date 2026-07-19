//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestQuickJSONPriorityError_n8xi is the error-contract teeth for beads-n8xi.
// `bd q`/`bd quick` honors the persistent --json on its success path
// (outputJSON(issue) at quick.go, direct-path parity landed by beads-j54e) and
// on the proxied path (runQuickProxiedServer) — but the --priority validation
// guard (quick.go, ValidatePriority failure) used a plain HandleError. Under
// `bd q "title" --priority=garbage --json` that produced EMPTY stdout + stderr
// text, unparseable by a --json consumer. The fix routes it through
// HandleErrorRespectJSON (0wp9/21xi/v02z/j54e --json-error-contract class).
//
// The guard fires BEFORE the usesProxiedServer() mode split and before any
// store access, so it is a deterministic, server-free error reachable with any
// invalid --priority — the teeth run bd as a subprocess and assert stdout is a
// parseable JSON object carrying the error, not empty.
func TestQuickJSONPriorityError_n8xi(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// The command verb is 'q' (quick.go Use: "q [title]"); exercise the two
	// invalid-priority shapes cobra's ValidatePriority rejects (non-numeric and
	// out-of-range word).
	cases := []struct {
		name string
		args []string
	}{
		{"garbage_priority", []string{"q", "some title", "--priority=garbage", "--json"}},
		{"nope_priority", []string{"q", "some title", "--priority=nope", "--json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			// Expected to FAIL (invalid priority) — err != nil is fine.
			if err == nil {
				t.Fatalf("`bd %s` unexpectedly succeeded\nstdout:\n%s", strings.Join(tc.args, " "), stdout.String())
			}

			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout is EMPTY on a failing `bd %s` — the error must be emitted as a JSON object on stdout (plain-text HandleError breaks parsers, beads-n8xi)\nstderr:\n%s", strings.Join(tc.args, " "), stderr.String())
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
