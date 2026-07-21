//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCheckReadonlyJSONError_EmitsStdoutObject is the end-to-end teeth for the
// shared read-only guard's --json contract (beads-rus7u). CheckReadonly (the
// single chokepoint gating ~105 write commands in --readonly worker-sandbox
// mode) used to always print bare plaintext to STDERR and os.Exit(1), producing
// an EMPTY stdout even under --json — so a scripted --json caller running any
// write command in a read-only sandbox got nothing parseable. The fix mirrors
// FatalErrorRespectJSON / the broz + 9fww sibling fixes: emit a structured
// {error} object on STDOUT under --json.
//
// `bd close --readonly --json` triggers CheckReadonly("close") deterministically
// (it is the FIRST statement in close's RunE, before any ID resolution). RED
// before the fix (empty stdout, plaintext stderr); GREEN after (JSON object with
// an "error" field on stdout). One test covers all 105 callers via the shared
// chokepoint.
func TestCheckReadonlyJSONError_EmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "close", "--readonly", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected non-zero exit for `close --readonly --json`, got success\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on `close --readonly --json` — the read-only guard must emit a JSON error object on stdout (json-error-contract beads-rus7u)\nstderr:\n%s",
			stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on `close --readonly --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"]
	if !ok {
		t.Fatalf("expected an \"error\" field in the read-only-guard --json stdout, got: %s", out)
	}
	if s, _ := msg.(string); !strings.Contains(s, "read-only mode") {
		t.Errorf("expected the error to name the read-only-mode restriction, got %q", s)
	}
}

// TestCheckReadonlyNonJSONUnchanged pins that WITHOUT --json the guard still
// prints the plaintext "Error: operation '...' is not allowed in read-only
// mode" to STDERR and exits 1 (no regression to human output). The message text
// is byte-for-byte unchanged by the rus7u fix.
func TestCheckReadonlyNonJSONUnchanged(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "close", "--readonly")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected non-zero exit for `close --readonly`, got success\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	if out := strings.TrimSpace(stdout.String()); out != "" {
		t.Errorf("expected EMPTY stdout for non-json read-only error, got:\n%s", out)
	}
	serr := stderr.String()
	if !strings.Contains(serr, "not allowed in read-only mode") {
		t.Errorf("expected plaintext read-only-mode error on stderr, got:\n%s", serr)
	}
	if strings.Contains(strings.TrimSpace(serr), "\"error\"") {
		t.Errorf("non-json path must NOT emit a JSON object, got:\n%s", serr)
	}
}
