//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestConfigSetJSON_ErrorPathEmitsStdoutObject is the error-contract teeth for
// beads-jjuv. config honors the persistent --json on its success paths (it has
// `if jsonOutput` blocks), but its error paths used plain HandleError (plain
// text on stderr, EMPTY stdout) — so under --json a failure produced empty
// stdout, breaking JSON parsers. This is the same error-contract half as
// beads-06km/lv51/9fww, but WITHOUT a flag-shadow (config has no command-local
// --json flag). The fix routes the error paths through HandleErrorRespectJSON,
// matching the canonical honored-json commands (list/show/update/close).
//
// The defect lives in cobra's RunE error return + PersistentPostRun JSON
// emission, so the teeth must run bd as a subprocess and assert stdout is a
// parseable JSON object with an "error" field. A pure-function test cannot
// catch it.
//
// `config set beads.role <invalid>` is a deterministic, server-free error
// (validated against an allowlist before any DB/git write), so it reliably
// reaches a HandleError path.
func TestConfigSetJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "config", "set", "beads.role", "NOTAVALIDROLE", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// Expected to FAIL (invalid role) — err != nil is fine.
	if err == nil {
		t.Fatalf("`config set beads.role NOTAVALIDROLE --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `config set --json` — the error must be emitted as a JSON object on stdout (plain-text HandleError breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `config set --json`: %v\nstdout:\n%s", jerr, out)
	}
	// The error message lives at the top level or under a "data" envelope.
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `config set --json` stdout, got: %s", out)
	}
}

// TestConfigSetRejectJSON_StdoutObject is the beads-taffx residual leg of jjuv.
// jjuv converted config's VALIDATION error paths (invalid role/type) to
// HandleErrorRespectJSON, but MISSED the protected-key + dolt.debug reject
// paths (config.go configSetCmd / configSetManyCmd), which stayed on bare
// fmt.Fprintln(os.Stderr,...)+SilentExit() — so `config set issue_prefix X
// --json` produced EMPTY stdout + exit 1, indistinguishable from a crash.
//
// issue_prefix is a protected key (rejectProtectedConfigKey) rejected before
// any DB/git write — a deterministic, server-free error, like the beads.role
// path above. This asserts the reject reaches stdout as a parseable JSON object.
func TestConfigSetRejectJSON_StdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)

	cases := []struct {
		name string
		args []string
	}{
		{"set protected key", []string{"config", "set", "issue_prefix", "foo", "--json"}},
		{"set-many protected key", []string{"config", "set-many", "issue_prefix=foo", "--json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, _ := bdInit(t, bd)
			cmd := exec.Command(bd, tc.args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err == nil {
				t.Fatalf("%v unexpectedly succeeded\nstdout:\n%s", tc.args, stdout.String())
			}

			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout is EMPTY on a failing protected-key `%v` — the reject must be emitted as a JSON object on stdout, not stderr-only+SilentExit\nstderr:\n%s", tc.args, stderr.String())
			}

			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object on `%v`: %v\nstdout:\n%s", tc.args, jerr, out)
			}
			msg, ok := obj["error"].(string)
			if !ok {
				if data, dok := obj["data"].(map[string]interface{}); dok {
					msg, ok = data["error"].(string)
				}
			}
			if !ok || msg == "" {
				t.Errorf("expected a non-empty \"error\" field in `%v` stdout, got: %s", tc.args, out)
			}
		})
	}
}
