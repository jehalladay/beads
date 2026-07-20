//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestDoltSetTestJSONErrorEmitsStdoutObject is the beads-le67m error-contract
// teeth (8lqh / zczxw setDoltConfig twin). `bd dolt set` and `bd dolt test` honor
// the persistent --json on their SUCCESS paths (outputJSON -> stdout), but their
// entry guards + value-validation legs hand-rolled `Fprintf(os.Stderr)+os.Exit(1)`,
// so under --json stdout was EMPTY and the error landed on stderr — unparseable by
// a --json consumer reading stdout. The legs now route through
// FatalErrorRespectJSON / FatalErrorWithHintRespectJSON: a single
// {error, [hint], schema_version} JSON object on STDOUT, exit 1.
//
// The embedded-mode guard ("not supported in embedded mode") is the leg reachable
// without a live sql-server, so a plain embedded `bd init` workspace exercises it.
// (The per-key value-validation legs inside setDoltConfig — bad port, empty
// database/host/user, unknown-key, etc. — sit behind this guard and require
// server mode; they are fixed in the same seam but not directly teethed here.)
func TestDoltSetTestJSONErrorEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cases := []struct {
		name string
		args []string
	}{
		{"set-embedded", []string{"dolt", "set", "port", "3307", "--json"}},
		{"test-embedded", []string{"dolt", "test", "--json"}},
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
				t.Fatalf("stdout is EMPTY on a failing `bd %s` — a --json error must be a JSON object on stdout (beads-le67m; plain-text/JSON-on-stderr breaks parsers)\nstderr:\n%s",
					strings.Join(tc.args, " "), stderr.String())
			}
			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object on a failing `bd %s`: %v\nstdout:\n%s",
					strings.Join(tc.args, " "), jerr, out)
			}
			msg, ok := obj["error"].(string)
			if !ok {
				if data, dok := obj["data"].(map[string]interface{}); dok {
					msg, ok = data["error"].(string)
				}
			}
			if !ok || msg == "" {
				t.Errorf("expected a non-empty \"error\" field in failing `bd %s` stdout, got: %s",
					strings.Join(tc.args, " "), out)
			}
		})
	}
}
