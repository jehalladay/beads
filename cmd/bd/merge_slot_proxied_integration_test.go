//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerMergeSlot proves bd merge-slot {create,check,acquire,release}
// no longer nil-panic in proxied-server mode (beads-i2v77, aocj proxied-routing
// class / highest-value refinery-mutex leg).
//
// merge_slot.go is NOT in noDbCommands and every handler used the nil global
// `store` (MergeSlotCreate/Check/Acquire/Release + storage.MergeSlotID) in
// proxiedServerMode → nil panic (structurally identical to the beads-jr2h4 bd
// branch and beads-2om1 bd count "storage is nil" repros).
//
// There is no proxied/UOW MergeSlot implementation, and the store factory
// refuses to open a direct store in proxied config ("proxy server store should
// be uow provider"). So, like `bd branch` / `compact --analyze` / `config set`,
// the correct behavior in proxied-server mode is to FAIL LOUD with a clear
// message (converting the panic to a clean, --json-contract-correct error),
// NOT to succeed. This test asserts that fail-loud contract on all four
// subcommands: no panic, a purpose-built message, and a valid JSON error object
// on stdout under --json.
func TestProxiedServerMergeSlot(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// Shared assertion: the run must FAIL cleanly (non-zero exit) with the
	// purpose-built proxied guard message and WITHOUT any panic / nil-pointer /
	// raw "storage is nil".
	assertCleanProxiedRefusal := func(t *testing.T, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-i2v77: bd merge-slot unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-i2v77: bd merge-slot PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-i2v77: expected the purpose-built 'not available in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	}

	subcmds := []string{"create", "check", "acquire", "release"}
	for _, sub := range subcmds {
		sub := sub
		t.Run("merge_slot_"+sub+"_refuses_cleanly", func(t *testing.T) {
			p := bdProxiedInit(t, bd, "ms"+sub[:2])
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "merge-slot", sub)
			assertCleanProxiedRefusal(t, stdout, stderr, err)
		})
	}

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh contract),
	// not plaintext and not a panic backtrace. Assert on every subcommand.
	for _, sub := range subcmds {
		sub := sub
		t.Run("merge_slot_"+sub+"_json_error_contract", func(t *testing.T) {
			p := bdProxiedInit(t, bd, "mj"+sub[:2])
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "merge-slot", sub, "--json")
			if err == nil {
				t.Fatalf("beads-i2v77: bd merge-slot %s --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", sub, stdout)
			}
			if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
				t.Fatalf("beads-i2v77: bd merge-slot %s --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", sub, stdout, stderr)
			}
			start := strings.Index(stdout, "{")
			if start < 0 {
				t.Fatalf("beads-i2v77: --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			var obj map[string]interface{}
			if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
				t.Fatalf("beads-i2v77: stdout is not a parseable JSON object: %v\nraw:\n%s", jerr, stdout[start:])
			}
			msg, ok := obj["error"].(string)
			if !ok || msg == "" {
				t.Errorf("beads-i2v77: expected a non-empty 'error' field in the --json error doc, got: %v", obj)
			}
			if !strings.Contains(msg, "proxied-server mode") {
				t.Errorf("beads-i2v77: expected the proxied guard message in the JSON error, got: %q", msg)
			}
		})
	}
}
