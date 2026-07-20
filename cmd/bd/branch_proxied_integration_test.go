//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerBranch proves bd branch no longer nil-panics in
// proxied-server mode (beads-jr2h4, aocj proxied-routing class / VCS-cmd leg).
// branch.go used the nil global `store` (ListBranches/CurrentBranch/Branch) in
// proxiedServerMode → nil panic (structurally identical to the beads-2om1 bd
// count "storage is nil" repro).
//
// Branch VCS ops manipulate the LOCAL Dolt working set — they have no
// proxied-UOW equivalent, and the store factory refuses to open a direct store
// in proxied config ("proxy server store should be uow provider"). So, like
// `compact --analyze` / `config set`, the correct behavior in proxied-server
// mode is to FAIL LOUD with a clear message (converting the panic to a clean,
// --json-contract-correct error), NOT to succeed. This test asserts that
// fail-loud contract: no panic, a purpose-built message, and a valid JSON error
// object on stdout under --json.
func TestProxiedServerBranch(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// A shared assertion: the run must FAIL cleanly (non-zero exit) with the
	// purpose-built proxied guard message and WITHOUT any panic / nil-pointer /
	// raw "storage is nil".
	assertCleanProxiedRefusal := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-jr2h4: bd branch unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-jr2h4: bd branch PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-jr2h4: expected the purpose-built 'not available in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	}

	t.Run("branch_list_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "brl")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "branch")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	t.Run("branch_create_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "brc")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "branch", "jr2h4-feature")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh contract),
	// not plaintext and not a panic backtrace.
	t.Run("branch_json_error_contract", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "brj")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "branch", "--json")
		if err == nil {
			t.Fatalf("beads-jr2h4: bd branch --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", stdout)
		}
		if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("beads-jr2h4: bd branch --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("beads-jr2h4: --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
			t.Fatalf("beads-jr2h4: stdout is not a parseable JSON object: %v\nraw:\n%s", jerr, stdout[start:])
		}
		msg, ok := obj["error"].(string)
		if !ok || msg == "" {
			t.Errorf("beads-jr2h4: expected a non-empty 'error' field in the --json error doc, got: %v", obj)
		}
		if !strings.Contains(msg, "proxied-server mode") {
			t.Errorf("beads-jr2h4: expected the proxied guard message in the JSON error, got: %q", msg)
		}
	})
}
