//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerRestore proves bd restore no longer nil-panics in
// proxied-server mode (beads-m0hme, aocj proxied-routing class / restore leg).
//
// restore.go is NOT in noDbCommands and every store call (GetIssue,
// GetCompactionSnapshot, RestoreFromSnapshot, History) dereferenced the nil
// global `store` in proxiedServerMode → nil panic (structurally identical to the
// beads-jr2h4 bd branch, beads-i2v77 bd merge-slot, and beads-2om1 bd count
// "storage is nil" repros).
//
// Unlike the aocj compact leg — which ext'd SnapshotIssueForCompaction/
// CompactOverwrite onto the proxied IssueUseCase — restore's core ops
// GetCompactionSnapshot/RestoreFromSnapshot are NOT on the UseCase interface:
// their backing issueops helpers (GetLatestSnapshotInTx/RestoreFromSnapshotInTx)
// require a concrete *sql.Tx for a destructive content-overwrite, the proxied
// UOW yields UseCases not a raw tx, and the store factory refuses to open a
// direct store in proxied config ("proxy server store should be uow provider").
// So, like `bd branch` / `bd merge-slot` / `compact --apply`, the correct
// behavior in proxied-server mode is to FAIL LOUD with a clear message
// (converting the panic to a clean, --json-contract-correct error), NOT to
// succeed. This test asserts that fail-loud contract on both the read-only
// display path and the destructive --apply path: no panic, a purpose-built
// message, and a valid JSON error object on stdout under --json.
func TestProxiedServerRestore(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// Shared assertion: the run must FAIL cleanly (non-zero exit) with the
	// purpose-built proxied guard message and WITHOUT any panic / nil-pointer /
	// raw "storage is nil".
	assertCleanProxiedRefusal := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-m0hme: bd restore unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-m0hme: bd restore PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-m0hme: expected the purpose-built 'not available in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	}

	// The guard fires before any issue lookup, so a nonexistent id is fine — we
	// are proving the proxied refusal, not restore semantics.
	t.Run("restore_display_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rsd")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "restore", "rsd-1")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	t.Run("restore_apply_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rsa")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "restore", "rsa-1", "--apply")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh contract),
	// not plaintext and not a panic backtrace. Assert on both paths.
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"restore_display_json_error_contract", []string{"restore", "rsj-1", "--json"}},
		{"restore_apply_json_error_contract", []string{"restore", "rsj-1", "--apply", "--json"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := bdProxiedInit(t, bd, "rsj")
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, tc.args...)
			if err == nil {
				t.Fatalf("beads-m0hme: bd %s unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", strings.Join(tc.args, " "), stdout)
			}
			if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
				t.Fatalf("beads-m0hme: bd %s panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", strings.Join(tc.args, " "), stdout, stderr)
			}
			start := strings.Index(stdout, "{")
			if start < 0 {
				t.Fatalf("beads-m0hme: --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
				t.Fatalf("beads-m0hme: stdout is not a parseable JSON object: %v\nraw:\n%s", jerr, stdout[start:])
			}
			msg, ok := obj["error"].(string)
			if !ok || msg == "" {
				t.Errorf("beads-m0hme: expected a non-empty 'error' field in the --json error doc, got: %v", obj)
			}
			if !strings.Contains(msg, "proxied-server mode") {
				t.Errorf("beads-m0hme: expected the proxied guard message in the JSON error, got: %q", msg)
			}
		})
	}
}
