//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerCompactDolt proves `bd compact` (the Dolt-history squash,
// NOT `bd admin compact`) no longer nil-panics in proxied-server mode
// (beads-bvug2, aocj proxied-routing class / maint leg).
//
// compact_dolt.go is NOT in noDbCommands and calls store.Log directly at the
// top of its RunE (compact_dolt.go:81). In proxied-server mode the global
// `store` is nil (main.go PersistentPreRun returns before newDoltStore), so
// that call nil-panicked (RED-proven SIGSEGV; sibling of the beads-jr2h4
// branch, beads-i2v77 merge-slot, and beads-6iwwf vc repros).
//
// Dolt history compaction manipulates the LOCAL Dolt store with no proxied/UOW
// equivalent, and the store factory refuses a direct store in proxied config.
// So, like `bd branch` / `bd vc` / `compact --analyze`, the correct behavior in
// proxied-server mode is to FAIL LOUD with a clear message, NOT to succeed.
func TestProxiedServerCompactDolt(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	assertCleanProxiedRefusal := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-bvug2: bd compact unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-bvug2: bd compact PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-bvug2: expected the purpose-built 'not available in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	}

	// Use --dry-run so the run never touches the (nonexistent) direct store even
	// on the pre-guard code path; the guard fires before store.Log regardless.
	t.Run("compact_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cmp")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "compact", "--dry-run")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh contract),
	// not plaintext and not a panic backtrace.
	t.Run("compact_json_error_contract", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cmj")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "compact", "--dry-run", "--json")
		if err == nil {
			t.Fatalf("beads-bvug2: bd compact --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", stdout)
		}
		if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("beads-bvug2: bd compact --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("beads-bvug2: --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
			t.Fatalf("beads-bvug2: stdout is not a parseable JSON object: %v\nraw:\n%s", jerr, stdout[start:])
		}
		msg, ok := obj["error"].(string)
		if !ok || msg == "" {
			t.Errorf("beads-bvug2: expected a non-empty 'error' field in the --json error doc, got: %v", obj)
		}
		if !strings.Contains(msg, "proxied-server mode") {
			t.Errorf("beads-bvug2: expected the proxied guard message in the JSON error, got: %q", msg)
		}
	})
}
