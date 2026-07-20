//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerMolBurnBondSquash proves the mutating mol subcommands
// mol burn / mol bond / mol squash fail loud with an accurate message in
// proxied-server mode instead of misdiagnosing the nil store as a missing
// local database (beads-ojyjj, aocj fail-loud class — the mutating mol-family
// siblings of mgjco's cook/pour/wisp that the aocj/fszd umbrellas never
// enumerated).
//
// GROUND TRUTH (cmd/bd @origin/main): in proxiedServerMode main.go's
// PersistentPreRunE sets the UOW provider and RETURNS before newDoltStore
// (main.go:1147-1155), so the global `store` stays nil. These commands are NOT
// in noDbCommands and reach a direct store call through a raw storage.Transaction:
//
//   - mol burn   (store.GetIssue + transact — molecule deletion)
//   - mol bond   (resolveOrCookToSubgraph + bondProtoMol transact)
//   - mol squash (store.GetIssue/loadTemplateSubgraph + squashMolecule transact)
//
// Pre-fix each carried a bare `if store == nil { "no database connection" }`
// guard that MISDIAGNOSES the proxied config as a missing local DB (the
// fo3c2/flatten wrong-diagnosis shape). All three WRITE via a raw
// storage.Transaction the proxied UOW does not yield, so the correct behavior
// is FAIL-LOUD with an accurate "not supported in proxied-server mode" message.
//
// Each subtest asserts the fail-loud contract AND — critically — that the
// message names proxied-server mode and does NOT leave the old misleading
// "no database connection" wording (the mutation-verify discriminator: neuter
// a guard and the wrong message / panic returns).
func TestProxiedServerMolBurnBondSquash(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	assertAccurateProxiedRefusal := func(t *testing.T, label, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-ojyjj: %s unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", label, stdout)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "panic") ||
			strings.Contains(combined, "nil pointer") ||
			strings.Contains(combined, "invalid memory address") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("beads-ojyjj: %s PANICKED/nil-crashed in proxied mode (should be a clean fail-loud error):\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
		if !strings.Contains(combined, "proxied-server mode") {
			t.Errorf("beads-ojyjj: %s expected the accurate 'not supported in proxied-server mode' guard message, got:\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
		// The whole point of ojyjj: the message must not misdiagnose the failure
		// as a missing local store. If a guard is neutered, control falls through
		// to the old store==nil path and this wording returns (or it nil-panics)
		// — this is the mutation-verify RED tell.
		if strings.Contains(combined, "no database connection") ||
			strings.Contains(combined, "no store available") {
			t.Errorf("beads-ojyjj: %s misdiagnoses the failure as a missing local store (should say proxied-server mode), got:\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
	}

	assertJSONErrorContract := func(t *testing.T, label, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("beads-ojyjj: %s --json unexpectedly SUCCEEDED in proxied mode:\nstdout:\n%s", label, stdout)
		}
		if strings.Contains(stdout+stderr, "panic") || strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("beads-ojyjj: %s --json panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("beads-ojyjj: %s --json error must be a JSON object on stdout, got:\nstdout:\n%s\nstderr:\n%s", label, stdout, stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stdout[start:])), &obj); jerr != nil {
			t.Fatalf("beads-ojyjj: %s stdout is not a parseable JSON object: %v\nraw:\n%s", label, jerr, stdout[start:])
		}
		msg, ok := obj["error"].(string)
		if !ok || msg == "" {
			t.Errorf("beads-ojyjj: %s expected a non-empty 'error' field in the --json error doc, got: %v", label, obj)
		}
		if !strings.Contains(msg, "proxied-server mode") {
			t.Errorf("beads-ojyjj: %s expected the accurate proxied guard message in the JSON error, got: %q", label, msg)
		}
		if strings.Contains(msg, "no database connection") || strings.Contains(msg, "no store available") {
			t.Errorf("beads-ojyjj: %s --json error misdiagnoses as a missing local store (mutation-verify tell), got: %q", label, msg)
		}
	}

	// Each case invokes with the minimum valid positional args so the proxied
	// guard (which fires before any store use) is what trips, not a cobra
	// arg-count error. burn/bond/squash are `mol` subcommands (molCmd.AddCommand).
	cases := []struct {
		label string
		args  []string
	}{
		{"mol burn", []string{"mol", "burn", "mol-nope", "--yes"}},
		{"mol bond", []string{"mol", "bond", "mol-nope", "mol-nope2"}},
		{"mol squash", []string{"mol", "squash", "mol-nope"}},
	}

	for _, tc := range cases {
		tc := tc
		safe := strings.NewReplacer(" ", "_", "-", "_").Replace(tc.label)
		t.Run(safe+"_refuses_with_accurate_message", func(t *testing.T) {
			p := bdProxiedInit(t, bd, "ob"+safe[:2])
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, tc.args...)
			assertAccurateProxiedRefusal(t, tc.label, stdout, stderr, err)
		})
		t.Run(safe+"_json_error_contract", func(t *testing.T) {
			p := bdProxiedInit(t, bd, "oj"+safe[:2])
			stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, append(tc.args, "--json")...)
			assertJSONErrorContract(t, tc.label, stdout, stderr, err)
		})
	}
}
