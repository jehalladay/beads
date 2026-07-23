//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestEmbeddedAddPeerPasswordRequiresUser_dnik6 proves `bd federation add-peer
// <name> <url> --password X` (WITHOUT --user) is REJECTED rather than silently
// dropping the password.
//
// Before the fix, runFederationAddPeer routed a userless add to store.AddRemote
// (the else branch fires when federationUser=="" && sov==""), which stores no
// credentials — so the supplied --password vanished with no warning or error on
// a security-adjacent path. The credential model is username+password keyed on
// username presence (withPeerCredentials authenticates only when both are
// non-empty), so a password without a username cannot authenticate and is
// meaningless; it must fail loud, not fail silent.
func TestEmbeddedAddPeerPasswordRequiresUser_dnik6(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt federation tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// The core defect: --password with no --user must error, not silently drop.
	t.Run("password_without_user_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "dnpwu0")

		out := bdFederationFail(t, bd, dir, "add-peer", "pw-peer", "file:///tmp/pw-peer",
			"--password", "secret")
		if !strings.Contains(out, "--password requires --user") {
			t.Errorf("REGRESSION (beads-dnik6): expected '--password requires --user', got: %s", out)
		}

		// And the peer must NOT have been created credential-free behind the error.
		listOut := bdFederation(t, bd, dir, "list-peers")
		if strings.Contains(listOut, "pw-peer") {
			t.Errorf("REGRESSION (beads-dnik6): peer was created despite the rejected --password (silent drop); list=%s", listOut)
		}
	})

	// --json variant: stdout carries a parseable JSON error object.
	t.Run("password_without_user_json_error", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "dnpwuj")

		out := bdFederationFail(t, bd, dir, "add-peer", "pw-peer-j", "file:///tmp/pw-peer-j",
			"--password", "secret", "--json")
		var obj map[string]any
		dec := json.NewDecoder(strings.NewReader(out))
		var parsed bool
		for {
			if e := dec.Decode(&obj); e != nil {
				break
			}
			if s, ok := obj["error"].(string); ok && s != "" {
				parsed = true
				break
			}
		}
		if !parsed {
			t.Errorf("REGRESSION (beads-dnik6): --password without --user --json did not emit a parseable JSON error with a non-empty \"error\"; out=%q", out)
		}
	})

	// Backward-compat: --user WITH --password still succeeds (guard is precise).
	t.Run("user_and_password_still_works", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "dnpwuok")

		out := bdFederation(t, bd, dir, "add-peer", "auth-ok", "file:///tmp/auth-ok",
			"--user", "admin", "--password", "secret", "--json")
		var result map[string]any
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("failed to parse JSON: %v\n%s", err, out)
		}
		if hasAuth, _ := result["has_auth"].(bool); !hasAuth {
			t.Errorf("--user + --password should store credentials (has_auth=true); out=%s", out)
		}
	})
}
