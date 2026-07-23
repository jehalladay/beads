//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestEmbeddedFederationOriginNotPeer_j785d proves that "origin" — beads' own
// git-backup remote (bd dolt push/pull), NOT a federation peer — is excluded
// from `bd federation list-peers` and `bd federation status` auto-discovery,
// matching the convention already enforced by `bd federation sync` and the
// doctor. Before the fix, status/list-peers listed origin as a peer (and status
// wasted a Fetch/reachability probe on it) — a peer that sync never touches.
//
// MUTATION-VERIFIED: drop the `if r.Name == "origin" { continue }` filter in
// either runFederationListPeers or runFederationStatus auto-discovery and the
// corresponding sub-test goes RED (origin reappears as a peer).
func TestEmbeddedFederationOriginNotPeer_j785d(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt federation tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	const realURL = "file:///tmp/j785d-real"
	const originURL = "file:///tmp/j785d-origin"

	// setup adds a genuine federation peer AND an "origin" remote (the
	// git-backup target), so auto-discovery has both to choose between.
	setup := func(t *testing.T, prefix string) string {
		t.Helper()
		dir, _, _ := bdInit(t, bd, "--prefix", prefix)
		bdFederation(t, bd, dir, "add-peer", "realpeer", realURL)
		bdFederation(t, bd, dir, "add-peer", "origin", originURL)
		return dir
	}

	t.Run("list_peers_excludes_origin", func(t *testing.T) {
		dir := setup(t, "j785dl")

		out := bdFederation(t, bd, dir, "list-peers", "--json")
		var peers []map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &peers); err != nil {
			t.Fatalf("parse list-peers JSON: %v\n%s", err, out)
		}
		names := map[string]bool{}
		for _, p := range peers {
			if n, _ := p["Name"].(string); n != "" {
				names[n] = true
			}
		}
		if !names["realpeer"] {
			t.Errorf("list-peers must include the real federation peer, got names %v\n%s", names, out)
		}
		if names["origin"] {
			t.Errorf("REGRESSION (beads-j785d): list-peers listed 'origin' as a federation peer; origin is the git-backup remote, got names %v\n%s", names, out)
		}
	})

	t.Run("status_excludes_origin", func(t *testing.T) {
		dir := setup(t, "j785ds")

		out := bdFederation(t, bd, dir, "status", "--json")
		var obj struct {
			Peers []struct {
				Status *struct {
					Peer string `json:"peer"`
				} `json:"status"`
				URL string `json:"url"`
			} `json:"peers"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
			t.Fatalf("parse status JSON: %v\n%s", err, out)
		}
		sawReal, sawOrigin := false, false
		for _, p := range obj.Peers {
			name := ""
			if p.Status != nil {
				name = p.Status.Peer
			}
			if name == "realpeer" || p.URL == realURL {
				sawReal = true
			}
			if name == "origin" || p.URL == originURL {
				sawOrigin = true
			}
		}
		if !sawReal {
			t.Errorf("status must include the real federation peer, got %s", out)
		}
		if sawOrigin {
			t.Errorf("REGRESSION (beads-j785d): status listed 'origin' as a federation peer; origin is the git-backup remote, got %s", out)
		}
	})

	t.Run("explicit_peer_origin_still_honored", func(t *testing.T) {
		dir := setup(t, "j785de")

		// --peer origin is an explicit opt-in (parity with sync): the filter
		// only applies to auto-discovery, so an explicit origin still renders.
		out := bdFederation(t, bd, dir, "status", "--peer", "origin", "--json")
		var obj struct {
			Peers []struct {
				URL string `json:"url"`
			} `json:"peers"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
			t.Fatalf("parse status --peer origin JSON: %v\n%s", err, out)
		}
		if len(obj.Peers) != 1 || obj.Peers[0].URL != originURL {
			t.Errorf("explicit --peer origin should render exactly origin, got %s", out)
		}
	})
}
