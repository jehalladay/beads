//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerCookPourWispFederation proves the mol-proto (cook/pour/wisp)
// and federation command families fail loud with an accurate message in
// proxied-server mode instead of nil-panicking or misdiagnosing the failure
// (beads-mgjco, aocj fail-loud class — the mol-proto + federation family the
// earlier aocj/fszd sweeps never enumerated).
//
// GROUND TRUTH (cmd/bd @origin/main): in proxiedServerMode main.go's
// PersistentPreRun sets the UOW provider and RETURNS before newDoltStore
// (main.go:1155), so the global `store` stays nil. These commands are NOT in
// noDbCommands and reach a direct `store.` call:
//
//   - cook --persist (persistCookFormula → store.GetIssue + transact)
//   - pour           (store.GetIssue + Pour via a raw storage.Transaction)
//   - wisp create    (store.SearchIssues/GetIssue + wisp-instantiation tx)
//   - federation {sync,status,add-peer,remove-peer,list-peers}
//     (store.AddFederationPeer / AddRemote / RemoveRemote / ListRemotes / Sync)
//
// The observed pre-fix symptom split (both wrong): cook/pour/wisp create and
// federation sync/status had a `store == nil` / getFederatedStore() guard that
// misdiagnosed the gap as a local "no database connection"/"no store available"
// (fo3c2 shape), while federation add-peer/remove-peer/list-peers were fully
// unguarded → a real nil-panic (i2v77 shape).
//
// Disposition = FAIL-LOUD for the whole family: the write paths run through a
// raw storage.Transaction the proxied UOW does not yield, and federation's
// headline (credentialed AddFederationPeer + Sync) lives only on the concrete
// DoltStore (credentials.go uses a *sql.Tx). There is no proxied/UOW mol-create
// or federation path, so the correct behavior mirrors merge-slot/restore: refuse
// with an accurate "not supported in proxied-server mode" message.
//
// Each subtest asserts the fail-loud contract AND — critically — that the
// message names proxied-server mode and does NOT leave the old misleading
// "no database connection" / "no store available" wording (the mutation-verify
// discriminator: neuter a guard and the wrong message / panic returns).
func TestProxiedServerCookPourWispFederation(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// A run must FAIL cleanly (non-zero exit) with the accurate proxied guard
	// message, WITHOUT any panic/nil-crash and WITHOUT the old misleading
	// local-store wording.
	assertAccurateProxiedRefusal := func(t *testing.T, label, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-mgjco: %s unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", label, stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-mgjco: %s PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-mgjco: %s expected the accurate 'not supported in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
		// The whole point of mgjco: the message must not misdiagnose the failure
		// as a missing local store. If a guard is neutered, control falls through
		// to the old store==nil / getFederatedStore() path and this wording
		// returns (or it nil-panics) — this is the mutation-verify RED tell.
		if strings.Contains(combined, "no database connection") ||
			strings.Contains(combined, "no store available") {
			t.Errorf("beads-mgjco: %s misdiagnoses the failure as a missing local store (should say proxied-server mode), got:\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
	}

	// --json must emit a single JSON error OBJECT on stdout (the 8lqh/927v
	// contract) carrying the accurate proxied message — not plaintext, not the
	// misleading local-store wording, not a panic backtrace.
	assertJSONErrorContract := func(t *testing.T, label, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-mgjco: %s --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", label, stdout)
		}
		if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("beads-mgjco: %s --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("beads-mgjco: %s --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
			t.Fatalf("beads-mgjco: %s stdout is not a parseable JSON object: %v\nraw:\n%s", label, jerr, stdout[start:])
		}
		msg, ok := obj["error"].(string)
		if !ok || msg == "" {
			t.Errorf("beads-mgjco: %s expected a non-empty 'error' field in the --json error doc, got: %v", label, obj)
		}
		if !strings.Contains(msg, "proxied-server mode") {
			t.Errorf("beads-mgjco: %s expected the accurate proxied guard message in the JSON error, got: %q", label, msg)
		}
		if strings.Contains(msg, "no database connection") || strings.Contains(msg, "no store available") {
			t.Errorf("beads-mgjco: %s --json error misdiagnoses as a missing local store (mutation-verify tell), got: %q", label, msg)
		}
	}

	// Each case invokes with the minimum valid positional args so the proxied
	// guard (which fires before any store use) is what trips, not a cobra
	// arg-count error.
	cases := []struct {
		label string
		args  []string
	}{
		{"cook --persist", []string{"cook", "mol-nope", "--persist"}},
		// pour and wisp are subcommands of `mol` (molCmd.AddCommand), not
		// top-level — cook is the only top-level one (rootCmd.AddCommand).
		{"mol pour", []string{"mol", "pour", "mol-nope"}},
		{"mol wisp create", []string{"mol", "wisp", "create", "mol-nope"}},
		{"federation sync", []string{"federation", "sync"}},
		{"federation status", []string{"federation", "status"}},
		{"federation add-peer", []string{"federation", "add-peer", "peer1", "http://example/db"}},
		{"federation remove-peer", []string{"federation", "remove-peer", "peer1"}},
		{"federation list-peers", []string{"federation", "list-peers"}},
	}

	for _, tc := range cases {
		tc := tc
		safe := strings.NewReplacer(" ", "_", "-", "_").Replace(tc.label)
		t.Run(safe+"_refuses_with_accurate_message", func(t *testing.T) {
			p := bdProxiedInit(t, bd, "mg"+safe[:2])
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, tc.args...)
			assertAccurateProxiedRefusal(t, tc.label, stdout, stderr, err)
		})
		t.Run(safe+"_json_error_contract", func(t *testing.T) {
			p := bdProxiedInit(t, bd, "mj"+safe[:2])
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, append(tc.args, "--json")...)
			assertJSONErrorContract(t, tc.label, stdout, stderr, err)
		})
	}
}
