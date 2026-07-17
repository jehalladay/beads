//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCompactDoltJSON_ErrorPathEmitsStdoutObject is the error-contract teeth for
// the beads-rg0c sweep, compact_dolt.go leg. `bd compact` (top-level, distinct
// from `bd admin compact`) honors the persistent --json on its success paths
// (4 outputJSON blocks) but its error paths used plain HandleError /
// HandleErrorWithHint — plain text on stderr, EMPTY stdout — so under --json a
// failure produced empty stdout, breaking JSON parsers. The fix routes those
// through HandleErrorRespectJSON / HandleErrorWithHintRespectJSON, matching the
// canonical honored-json commands (list/show/update/close).
//
// The defect lives in cobra's RunE error return + JSON emission, so the teeth
// run bd as a subprocess and assert stdout is a parseable JSON object with an
// "error" field. `compact --days -1 --json` is a deterministic, server-free
// error (validated before any store.Log call), so it reliably reaches a
// HandleError path.
func TestCompactDoltJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "compact", "--days", "-1", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// Expected to FAIL (--days must be non-negative) — err != nil is fine.
	if err == nil {
		t.Fatalf("`compact --days -1 --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `compact --json` — the error must be emitted as a JSON object on stdout (plain-text HandleError breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `compact --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `compact --json` stdout, got: %s", out)
	}
}
