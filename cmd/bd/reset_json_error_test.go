//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestResetJSONError_RequireServerModeEmitsStdoutObject is the end-to-end teeth
// for the `bd admin reset --json` error contract (beads-broz). runReset's FIRST
// error is requireServerMode: in embedded (non-server) mode it returns
// "'bd admin reset' is not yet supported in embedded mode". Before the fix that
// early-return used a bare HandleError, so under --json a scripted caller got
// plaintext on stderr and an EMPTY stdout — even though the very next error path
// (the "not a git repository" check) and every downstream reset path already
// honored --json. The fix mirrors the sibling path: emit {error} on stdout +
// SilentExit under --json.
//
// buildEmbeddedBD builds bd in embedded mode, so `bd admin reset --force --json`
// hits requireServerMode failure deterministically. RED before the fix (empty
// stdout); GREEN after (JSON object with an "error" field on stdout).
func TestResetJSONError_RequireServerModeEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "admin", "reset", "--force", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected non-zero exit for `admin reset --force --json` in embedded mode, got success\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on `reset --json` requireServerMode error — must emit a JSON error object on stdout (json-error-contract beads-broz)\nstderr:\n%s",
			stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on `reset --json` error: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"]
	if !ok {
		t.Fatalf("expected an \"error\" field in `reset --json` error stdout, got: %s", out)
	}
	if s, _ := msg.(string); !strings.Contains(s, "embedded mode") {
		t.Errorf("expected the error to name the embedded-mode limitation, got %q", s)
	}
}
