//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerGC proves bd gc no longer nil-panics in proxied-server mode
// (beads-0lunb, aocj proxied-routing class / maintenance-cmd leg). gc.go used the
// nil global `store` (SearchIssues/DeleteIssue/Log + UnwrapStore(store).DoltGC) in
// proxiedServerMode → nil panic (structurally identical to the beads-jr2h4 branch
// leg and the beads-i2v77 merge-slot leg).
//
// gc's disposition is WHOLE-COMMAND fail-loud (not per-leg split): it is
// documented as lifecycle GC "for standalone Beads databases", its headline
// DoltGC space-reclaim is an inherently local Dolt op with no proxied/UOW
// equivalent (like flatten's GC leg), and its decay phase is a bulk destructive
// DeleteIssue that has no business running at a hub-connected crew. So, like
// bd branch / bd merge-slot / compact --analyze, the correct behavior in
// proxied-server mode is to FAIL LOUD with a clear message (converting the panic
// to a clean, --json-contract-correct error), NOT to succeed. This test asserts
// that fail-loud contract: no panic, a purpose-built message, and a valid JSON
// error object on stdout under --json.
func TestProxiedServerGC(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// A shared assertion: the run must FAIL cleanly (non-zero exit) with the
	// purpose-built proxied guard message and WITHOUT any panic / nil-pointer /
	// raw "storage is nil".
	assertCleanProxiedRefusal := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-0lunb: bd gc unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-0lunb: bd gc PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-0lunb: expected the purpose-built 'not available in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	}

	// The default full run reaches the decay/compact/DoltGC store calls.
	t.Run("gc_full_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gcf")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gc", "--force")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	// --dry-run still touches the store (SearchIssues/Log) before printing, so it
	// must refuse too — the guard sits ahead of every store use, not behind a
	// mutating gate.
	t.Run("gc_dry_run_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gcd")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gc", "--dry-run")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh contract),
	// not plaintext and not a panic backtrace.
	t.Run("gc_json_error_contract", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "gcj")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "gc", "--force", "--json")
		if err == nil {
			t.Fatalf("beads-0lunb: bd gc --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", stdout)
		}
		if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("beads-0lunb: bd gc --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("beads-0lunb: --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
			t.Fatalf("beads-0lunb: stdout is not a parseable JSON object: %v\nraw:\n%s", jerr, stdout[start:])
		}
		msg, ok := obj["error"].(string)
		if !ok || msg == "" {
			t.Errorf("beads-0lunb: expected a non-empty 'error' field in the --json error doc, got: %v", obj)
		}
		if !strings.Contains(msg, "proxied-server mode") {
			t.Errorf("beads-0lunb: expected the proxied guard message in the JSON error, got: %q", msg)
		}
	})
}
