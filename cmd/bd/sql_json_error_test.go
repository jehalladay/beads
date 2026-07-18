//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestSqlJSON_EmbeddedModeErrorEmitsStdoutObject is the error-contract teeth for
// beads-y2yo (0wp9/xwjg/8lqh --json-error-contract class). `bd sql` honors the
// persistent --json on its success path (sql.go emits a JSON array/object), and
// its later error returns (nil store / no RawDBAccessor / nil underlying DB) all
// use HandleErrorRespectJSON — but the earliest guard, the embedded-mode
// rejection (`!usesSQLServer()` → "not yet supported in embedded mode"), used a
// plain HandleError: plain text on stderr, EMPTY stdout, so a --json consumer
// could not parse the failure. The fix routes it through HandleErrorRespectJSON,
// matching the three sibling returns immediately below it.
//
// Embedded is the default (non-server) mode — bdEnv strips all BEADS_ env, so
// `bd sql --json` here reliably reaches the embedded-mode guard.
func TestSqlJSON_EmbeddedModeErrorEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "sql", "--json", "SELECT 1")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// Expected to FAIL — `bd sql` is not supported in embedded mode.
	if err == nil {
		t.Fatalf("`bd sql --json` unexpectedly succeeded in embedded mode\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `bd sql --json` in embedded mode — the error must be a JSON object on stdout (beads-y2yo; plain-text HandleError breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `bd sql --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `bd sql --json` stdout, got: %s", out)
	}
}
