//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerFlatten proves `bd flatten` refuses cleanly in proxied-server
// mode with a PURPOSE-BUILT message, not the misleading "storage backend does
// not support flatten" (beads-94s8d, aocj proxied-routing class / maintenance
// leg — sibling of branch beads-jr2h4 + gc beads-0lunb).
//
// In proxied-server mode main.go PersistentPreRun returns before newDoltStore,
// leaving the global `store` nil (flatten is not in noDbCommands). Before the
// fix, storage.UnwrapStore(store).(storage.Flattener) yielded ok=false and
// flatten reported "storage backend does not support flatten" — a misleading
// backend-capability error, since the Dolt backend supports flatten fine and
// store is nil only because of proxied mode. Flatten's history-squash + DoltGC
// are inherently LOCAL Dolt maintenance ops with no proxied/UOW equivalent, so
// the correct behavior (matching branch/gc/compact) is to FAIL LOUD with the
// clear "not available in proxied-server mode" message. This test asserts that
// contract across the dry-run, force, and --json paths.
func TestProxiedServerFlatten(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	assertCleanProxiedRefusal := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-94s8d: bd flatten unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-94s8d: bd flatten PANICKED/nil-crashed in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		// The misleading pre-fix message must NOT appear — it implies the backend
		// can't flatten, when the real reason is proxied mode.
		if strings.Contains(combined, "storage backend does not support flatten") {
			t.Errorf("beads-94s8d: got the MISLEADING backend-capability error; expected the proxied-mode guard message:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-94s8d: expected the purpose-built 'not available in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	}

	// dry-run skips CheckReadonly but still hits the (nil) store — the guard must
	// fire ahead of it.
	t.Run("flatten_dryrun_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "fld")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "flatten", "--dry-run")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	t.Run("flatten_force_refuses_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "flf")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "flatten", "--force")
		assertCleanProxiedRefusal(t, stdout, stderr, err)
	})

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh contract),
	// carrying the proxied guard message.
	t.Run("flatten_json_error_contract", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "flj")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "flatten", "--dry-run", "--json")
		if err == nil {
			t.Fatalf("beads-94s8d: bd flatten --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", stdout)
		}
		if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("beads-94s8d: bd flatten --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("beads-94s8d: --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
			t.Fatalf("beads-94s8d: stdout is not a parseable JSON object: %v\nraw:\n%s", jerr, stdout[start:])
		}
		msg, ok := obj["error"].(string)
		if !ok || msg == "" {
			t.Errorf("beads-94s8d: expected a non-empty 'error' field in the --json error doc, got: %v", obj)
		}
		if !strings.Contains(msg, "proxied-server mode") {
			t.Errorf("beads-94s8d: expected the proxied guard message in the JSON error, got: %q", msg)
		}
	})
}
