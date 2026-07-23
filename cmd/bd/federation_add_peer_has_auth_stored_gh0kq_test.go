//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestEmbeddedAddPeerHasAuthReflectsStoredState_gh0kq proves that `bd
// federation add-peer` reports has_auth (and the human "credentials stored"
// line) from the peer's STORED credential state, not from whether THIS
// invocation happened to pass --user.
//
// Since beads-9mg09 (COALESCE credentials on re-add), a re-add of an already-
// authenticated peer that changes only --sovereignty or the URL PRESERVES the
// stored username/password. But the output block derived has_auth from
// `federationUser != ""`, so such a re-add (which omits --user) reported
// has_auth:false — misreporting a still-authenticated peer as credential-free
// on a security-adjacent surface. Automation keying on has_auth would wrongly
// conclude the peer lost its credentials. The fix reads the peer back
// (store.GetFederationPeer) and reports auth from the stored username.
//
// MUTATION-VERIFIED: revert has_auth back to `federationUser != ""` and the
// credential-preserving re-add step (has_auth:true) goes RED.
func TestEmbeddedAddPeerHasAuthReflectsStoredState_gh0kq(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt federation tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// The core defect: an authenticated peer re-added to change only the tier
	// (no --user) must still report has_auth:true, because 9mg09 preserved the
	// stored credentials.
	t.Run("cred_preserving_readd_reports_has_auth_true", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ghauth0")

		// 1. Add an authenticated peer.
		out := bdFederation(t, bd, dir, "add-peer", "gp", "file:///tmp/gh0kq-gp",
			"--user", "alice", "--password", "s3cr3t", "--json")
		var first map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &first); err != nil {
			t.Fatalf("parse first add-peer JSON: %v\n%s", err, out)
		}
		if first["has_auth"] != true {
			t.Fatalf("precondition: authenticated first add should report has_auth:true, got %v", first["has_auth"])
		}

		// 2. Re-add the SAME peer changing ONLY --sovereignty (no --user): 9mg09
		//    preserves the stored credentials, so has_auth must stay true.
		out = bdFederation(t, bd, dir, "add-peer", "gp", "file:///tmp/gh0kq-gp",
			"--sovereignty", "T2", "--json")
		var reAdd map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reAdd); err != nil {
			t.Fatalf("parse re-add JSON: %v\n%s", err, out)
		}
		if reAdd["has_auth"] != true {
			t.Errorf("REGRESSION (beads-gh0kq): credential-preserving re-add reported has_auth:%v, want true (9mg09 kept the stored credentials)", reAdd["has_auth"])
		}
		if reAdd["sovereignty"] != "T2" {
			t.Errorf("re-add sovereignty = %v, want T2", reAdd["sovereignty"])
		}
	})

	// Backward-compat: a first add with ONLY --sovereignty (no credentials ever
	// supplied) must report has_auth:false — the fix reports STORED state, and
	// no credentials were stored.
	t.Run("sovereignty_only_first_add_reports_has_auth_false", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ghauth1")

		out := bdFederation(t, bd, dir, "add-peer", "sp", "file:///tmp/gh0kq-sp",
			"--sovereignty", "T3", "--json")
		var obj map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
			t.Fatalf("parse sovereignty-only add JSON: %v\n%s", err, out)
		}
		if obj["has_auth"] != false {
			t.Errorf("sovereignty-only first add reported has_auth:%v, want false (no credentials stored)", obj["has_auth"])
		}
	})

	// Backward-compat: a first authenticated add still reports has_auth:true.
	t.Run("authenticated_first_add_reports_has_auth_true", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ghauth2")

		out := bdFederation(t, bd, dir, "add-peer", "ap", "file:///tmp/gh0kq-ap",
			"--user", "bob", "--password", "pw", "--json")
		var obj map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
			t.Fatalf("parse authenticated add JSON: %v\n%s", err, out)
		}
		if obj["has_auth"] != true {
			t.Errorf("authenticated first add reported has_auth:%v, want true", obj["has_auth"])
		}
	})
}
