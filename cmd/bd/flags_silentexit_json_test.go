//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCreateJSON_ConflictingDescFlagsEmitsStdoutObject is the error-contract
// teeth for beads-ew48y (7zh1h sibling). The multi-flag-conflict guards in the
// shared body/description parser getDescriptionFlag (flags.go ~L105/134/146)
// printed a multi-line "cannot specify both/multiple ... with different values"
// diagnostic to STDERR then SilentExit() — so under the persistent --json a
// parser reading stdout got EMPTY stdout (no JSON error object), the same
// parser-breaking class 7zh1h fixed for the plain-HandleError sites in this
// helper. This is a DISTINCT shape (multi-line listing, not a single
// HandleError), so the fix emits a flattened structured HandleErrorRespectJSON
// under --json while keeping the human multi-line stderr form otherwise.
//
// `create --json --description X --body Y` (different values) deterministically
// reaches the body-vs-firstFlag guard during flag handling, before any store
// mutation. The defect lives in cobra's RunE error return + JSON emission, so
// the teeth run bd as a subprocess and assert stdout is a parseable JSON object
// with a non-empty "error" field.
func TestCreateJSON_ConflictingDescFlagsEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "create", "A title", "--json", "--description", "one thing", "--body", "a different thing")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// Expected to FAIL (conflicting --description/--body values).
	if err == nil {
		t.Fatalf("`create --json --description X --body Y` (different values) unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `create --json` conflicting-desc-flags error — the error must be a JSON object on stdout (multi-line SilentExit stderr breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `create --json` conflicting-desc-flags error: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `create --json` conflicting-desc-flags stdout, got: %s", out)
	}
}
