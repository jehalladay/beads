//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerRecomputeBlocked proves bd recompute-blocked reports an
// ACCURATE fail-loud error in proxied-server mode (beads-fo3c2, aocj fail-loud
// class / recompute-blocked member).
//
// recompute_blocked.go is NOT in noDbCommands. In proxied-server mode main.go's
// PersistentPreRun returns early (main.go:1147) with the global store nil, so
// UnwrapStore(store)=nil → the `storage.BlockedRecomputer` assertion returns
// ok=false → the command historically emitted "storage backend does not support
// is_blocked recompute". Unlike branch/merge-slot/vc this was NOT a panic (the
// type-assertion shields it), but the message MISDIAGNOSES the failure: it
// blames the storage backend when the real gap is that the proxied path was
// never wired for this command.
//
// The fix adds a proxied guard BEFORE the UnwrapStore check that fails loud with
// an accurate message ("not supported in proxied-server mode"). This test
// asserts the fail-loud contract AND — critically — that the ACCURATE message is
// used, NOT the misleading "backend does not support" wording (which is the
// mutation-verify discriminator: neuter the guard and the wrong message returns).
func TestProxiedServerRecomputeBlocked(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("recompute_blocked_refuses_with_accurate_message", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rbacc")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "recompute-blocked")
		if err == nil {
			t.Fatalf("beads-fo3c2: bd recompute-blocked unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-fo3c2: bd recompute-blocked PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-fo3c2: expected the accurate 'not supported in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		// The whole point of fo3c2: the message must NOT blame the backend. If the
		// guard is neutered, control falls through to the UnwrapStore assertion and
		// this misleading wording returns — this is the mutation-verify RED tell.
		if strings.Contains(combined, "backend does not support") {
			t.Errorf("beads-fo3c2: message misdiagnoses the failure as an unsupported backend (should say proxied-server mode instead), got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh/927v
	// contract), carrying the accurate proxied message — not plaintext, not the
	// misleading backend wording, not a panic backtrace.
	t.Run("recompute_blocked_json_error_contract", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rbjsn")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "recompute-blocked", "--json")
		if err == nil {
			t.Fatalf("beads-fo3c2: bd recompute-blocked --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", stdout)
		}
		if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("beads-fo3c2: bd recompute-blocked --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("beads-fo3c2: --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
			t.Fatalf("beads-fo3c2: stdout is not a parseable JSON object: %v\nraw:\n%s", jerr, stdout[start:])
		}
		msg, ok := obj["error"].(string)
		if !ok || msg == "" {
			t.Errorf("beads-fo3c2: expected a non-empty 'error' field in the --json error doc, got: %v", obj)
		}
		if !strings.Contains(msg, "proxied-server mode") {
			t.Errorf("beads-fo3c2: expected the accurate proxied guard message in the JSON error, got: %q", msg)
		}
		if strings.Contains(msg, "backend does not support") {
			t.Errorf("beads-fo3c2: --json error misdiagnoses as unsupported backend (mutation-verify tell), got: %q", msg)
		}
	})
}
