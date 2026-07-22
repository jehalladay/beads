package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// beads-aza6m: handleFreshCloneError runs inside main.go's PersistentPreRunE
// newDoltStore error branch, ahead of EVERY subcommand including --json ones,
// and its caller then os.Exit(1)s. Before the fix it wrote plaintext to stderr
// unconditionally, so `bd list --json` on a fresh clone / uninitialized DB
// produced rc=1 with EMPTY stdout + plaintext stderr — breaking a scripted
// `bd ... --json | jq` (the 8lqh --json-contract class; same defect eng_1's
// beads-5v4ku fixed for the blocked-env-var leg one block up, whose adjacent
// schema-skew / remote-migrate-gate siblings already gate on `if jsonOutput`).
//
// The fix lives inside handleFreshCloneError (reads the package global
// jsonOutput + takes err), so it is exercised directly — no subprocess needed.

// freshCloneErr synthesizes an error that isFreshCloneError matches: it must
// contain BOTH the post-migration-validation marker AND the missing-issue_prefix
// marker (main_errors.go isFreshCloneError).
func freshCloneErr() error {
	return errors.New("post-migration validation failed: required config key missing: issue_prefix")
}

// captureStdoutStderrBool runs fn with os.Stdout/os.Stderr redirected to pipes
// and returns their contents plus fn's bool. Serialized on stdioMutex like the
// other stdio-capturing helpers in this package.
func captureStdoutStderrBool(t *testing.T, fn func() bool) (stdout, stderr string, ret bool) {
	t.Helper()
	stdioMutex.Lock()
	defer stdioMutex.Unlock()

	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	ret = fn()

	wOut.Close()
	wErr.Close()
	var bufOut, bufErr bytes.Buffer
	bufOut.ReadFrom(rOut)
	bufErr.ReadFrom(rErr)
	os.Stdout, os.Stderr = oldOut, oldErr
	return bufOut.String(), bufErr.String(), ret
}

// TestFreshCloneErrorJSONOnStdout_aza6m pins the --json contract: under
// jsonOutput the fresh-clone rejection must be a parseable JSON error on STDOUT
// (so `| jq` sees it), not plaintext on stderr with empty stdout.
func TestFreshCloneErrorJSONOnStdout_aza6m(t *testing.T) {
	old := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = old }()

	stdout, stderr, handled := captureStdoutStderrBool(t, func() bool {
		return handleFreshCloneError(freshCloneErr())
	})

	if !handled {
		t.Fatalf("handleFreshCloneError should return true for a fresh-clone error")
	}
	out := strings.TrimSpace(stdout)
	if out == "" {
		t.Fatalf("stdout is EMPTY under --json — the fresh-clone rejection must be a JSON object on stdout, not plaintext on stderr (beads-aza6m)\nstderr:\n%s", stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("stdout is not a JSON object under --json: %v\nstdout:\n%s\n(plaintext fresh-clone leak has regressed)", err, out)
	}
	msg, ok := extractJSONErrorMessage_aza6m(obj)
	if !ok {
		t.Fatalf("JSON error object should carry an 'error' field (top-level or under 'data'), got %v", obj)
	}
	if !strings.Contains(strings.ToLower(msg), "not initialized") &&
		!strings.Contains(strings.ToLower(msg), "fresh clone") {
		t.Errorf("error message should describe the fresh-clone / not-initialized condition, got %q", msg)
	}
}

// TestFreshCloneErrorPlaintextWhenNotJSON_aza6m is the parity negative: with
// --json OFF the guidance stays plaintext on stderr with empty stdout
// (unchanged behavior for interactive users).
func TestFreshCloneErrorPlaintextWhenNotJSON_aza6m(t *testing.T) {
	old := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = old }()

	stdout, stderr, handled := captureStdoutStderrBool(t, func() bool {
		return handleFreshCloneError(freshCloneErr())
	})

	if !handled {
		t.Fatalf("handleFreshCloneError should return true for a fresh-clone error")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("non-json path: stdout should be empty (plaintext goes to stderr), got %q", stdout)
	}
	if !strings.Contains(stderr, "Database not initialized") {
		t.Errorf("non-json path: stderr should carry the plaintext fresh-clone guidance, got %q", stderr)
	}
}

// TestFreshCloneErrorIgnoresNonMatch_aza6m guards the gate: a non-fresh-clone
// error returns false and emits nothing on either stream (both --json states).
func TestFreshCloneErrorIgnoresNonMatch_aza6m(t *testing.T) {
	for _, js := range []bool{true, false} {
		old := jsonOutput
		jsonOutput = js
		stdout, stderr, handled := captureStdoutStderrBool(t, func() bool {
			return handleFreshCloneError(errors.New("some unrelated open error"))
		})
		jsonOutput = old
		if handled {
			t.Errorf("jsonOutput=%v: non-fresh-clone error should not be handled", js)
		}
		if strings.TrimSpace(stdout) != "" || strings.TrimSpace(stderr) != "" {
			t.Errorf("jsonOutput=%v: non-fresh-clone error should emit nothing; stdout=%q stderr=%q", js, stdout, stderr)
		}
	}
}

// extractJSONErrorMessage_aza6m reads the "error" string from either a
// top-level object or a {"data": {...}} envelope, matching the canonical
// honored-json error shape (mirrors extractJSONErrorMessage_5v4ku).
func extractJSONErrorMessage_aza6m(obj map[string]interface{}) (string, bool) {
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
