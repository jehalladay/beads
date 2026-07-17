//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestRestoreJSONError_EmitsStdoutObject is the teeth for the restore
// json-error-contract fix. `bd restore` registers its own --json flag and emits
// every success payload via outputJSON to stdout, but its error paths
// historically did fmt.Fprintf(os.Stderr,...) + os.Exit(1), leaving stdout
// EMPTY under --json. A JSON consumer then cannot tell an error from a decode
// failure. After the fix, error paths route through FatalErrorRespectJSON /
// FatalErrorWithHintRespectJSON, which emit a structured {"error":...} object
// on stdout under --json.
//
// This exercises the simplest deterministic error path — restoring a
// nonexistent issue — end-to-end through the real os.Exit code path (which a
// normal in-process test cannot catch).
func TestRestoreJSONError_EmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "restore", "beads-does-not-exist", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected non-zero exit restoring a nonexistent issue --json, got success\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on restore --json error — must emit a JSON error object on stdout (json-error-contract)\nstderr:\n%s",
			stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on restore --json error: %v\nstdout:\n%s", jerr, out)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in restore --json error stdout, got: %s", out)
	}
}
