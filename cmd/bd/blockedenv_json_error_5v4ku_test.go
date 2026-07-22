//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestBlockedEnvVarJSONError_5v4ku is the end-to-end tooth for beads-5v4ku.
// checkBlockedEnvVars() runs inside main.go's PersistentPreRunE, ahead of EVERY
// subcommand — including --json ones. It previously routed its rejection through
// a bare HandleError (plaintext → stderr), so `BD_BACKEND=x bd list --json`
// produced rc=1 with EMPTY stdout and a plaintext "Error: ..." on stderr,
// breaking any parser reading the stdout JSON contract (the 8lqh class). The fix
// switches that leg to HandleErrorRespectJSON; jsonOutput is already bound from
// the --json PersistentFlag (flag-parse precedes PersistentPreRunE), so under an
// explicit --json the guard emits {error,schema_version} on stdout.
//
// This cannot be a pure unit test: the defect lives in the PersistentPreRunE
// closure (main.go:629), which runs inside cobra's Execute plumbing, so the
// teeth run real bd as a subprocess and assert stdout is a parseable JSON error.
func TestBlockedEnvVarJSONError_5v4ku(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Both blocked vars, exercised through a --json-capable read command.
	cases := []struct {
		name    string
		blocked string
	}{
		{"BD_BACKEND", "BD_BACKEND"},
		{"BD_DATABASE_BACKEND", "BD_DATABASE_BACKEND"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, "list", "--json")
			cmd.Dir = dir
			cmd.Env = append(bdEnv(dir), tc.blocked+"=dolt")
			stdout, stderr, err := runCommandBuffers(t, cmd)

			// Must FAIL (non-zero) — a blocked env var is a hard rejection.
			if err == nil {
				t.Fatalf("`bd list --json` with %s set unexpectedly succeeded (exit 0)\nstdout:\n%s",
					tc.blocked, stdout.String())
			}

			out := strings.TrimSpace(stdout.String())
			if out == "" {
				t.Fatalf("stdout is EMPTY on `bd list --json` with %s set — the blocked-env rejection must be a JSON object on stdout, not plaintext on stderr (beads-5v4ku)\nstderr:\n%s",
					tc.blocked, stderr.String())
			}

			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
				t.Fatalf("stdout is not a JSON object on `bd list --json` with %s set: %v\nstdout:\n%s\n(plaintext blocked-env leak has regressed)",
					tc.blocked, jerr, out)
			}

			msg, ok := extractJSONErrorMessage_5v4ku(obj)
			if !ok {
				t.Fatalf("JSON error object should carry an 'error' field (top-level or under 'data'), got %v", obj)
			}
			if !strings.Contains(msg, tc.blocked) {
				t.Errorf("error message should name the blocked var %q, got %q", tc.blocked, msg)
			}
			if !strings.Contains(msg, "not supported") {
				t.Errorf("error message should preserve the checkBlockedEnvVars text, got %q", msg)
			}
		})
	}
}

// Parity negative: with --json OFF, the blocked-env rejection stays plaintext on
// stderr with empty stdout (unchanged behavior for interactive users).
func TestBlockedEnvVarPlaintextWhenNotJSON_5v4ku(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "list")
	cmd.Dir = dir
	cmd.Env = append(bdEnv(dir), "BD_BACKEND=dolt")
	stdout, stderr, err := runCommandBuffers(t, cmd)

	if err == nil {
		t.Fatalf("`bd list` with BD_BACKEND set unexpectedly succeeded (exit 0)\nstdout:\n%s", stdout.String())
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Errorf("non-json path: stdout should be empty (plaintext goes to stderr), got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "BD_BACKEND") || !strings.Contains(stderr.String(), "not supported") {
		t.Errorf("non-json path: stderr should carry the plaintext blocked-env error, got %q", stderr.String())
	}
}

// extractJSONErrorMessage_5v4ku reads the "error" string from either a
// top-level object or a {"data": {...}} envelope, matching the canonical
// honored-json error shape.
func extractJSONErrorMessage_5v4ku(obj map[string]interface{}) (string, bool) {
	if msg, ok := obj["error"].(string); ok {
		return msg, true
	}
	if data, ok := obj["data"].(map[string]interface{}); ok {
		if msg, ok := data["error"].(string); ok {
			return msg, true
		}
	}
	return "", false
}
