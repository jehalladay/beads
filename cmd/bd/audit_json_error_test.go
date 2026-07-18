//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestAuditRecordJSON_ErrorPathEmitsStdoutObject is the error-contract teeth for
// the beads-rg0c sweep, audit.go leg. `bd audit record` honors the persistent
// --json on its success path (outputJSON with id/kind) but its error paths used
// plain HandleError — plain text on stderr, EMPTY stdout — so under --json a
// failure produced empty stdout, breaking JSON parsers. The fix routes those
// through HandleErrorRespectJSON, matching the canonical honored-json commands
// (list/show/update/close).
//
// The defect lives in cobra's RunE error return + JSON emission, so the teeth
// run bd as a subprocess and assert stdout is a parseable JSON object with an
// "error" field. `audit record --json` with no --kind is a deterministic,
// server-free error (validated before any audit.Append call), so it reliably
// reaches a HandleError path.
func TestAuditRecordJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "audit", "record", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// Expected to FAIL (--kind is required) — err != nil is fine.
	if err == nil {
		t.Fatalf("`audit record --json` (no --kind) unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `audit record --json` — the error must be emitted as a JSON object on stdout (plain-text HandleError breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `audit record --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `audit record --json` stdout, got: %s", out)
	}
}
