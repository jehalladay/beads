package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// The remove-peer guard test reuses federationAddPeerFakeStore (defined in
// federation_add_peer_sovereignty_pib1g_test.go) but needs to observe the
// REMOVE dispatch too. Methods on the type can live in any file of the package,
// so record the remove calls here.
func (f *federationAddPeerFakeStore) RemoveFederationPeer(_ context.Context, name string) error {
	f.removeFederationPeerCalls = append(f.removeFederationPeerCalls, name)
	return nil
}

func (f *federationAddPeerFakeStore) RemoveRemote(_ context.Context, name string) error {
	f.removeRemoteCalls = append(f.removeRemoteCalls, name)
	return nil
}

// TestFederationRemovePeerRejectsReservedOrigin guards beads-81a3n, the remove
// side of the same clobber beads-jkbyt guards on add: `bd federation remove-peer
// origin` routes through store.RemoveFederationPeer, which removes the backing
// "origin" Dolt remote best-effort (RemoveRemote) — the very remote bd dolt
// push/pull depend on. Post-jkbyt there is no legitimate "origin" peer, so the
// command layer must refuse the reserved name (case-insensitively) BEFORE any
// store dispatch.
//
// Load-bearing: the assertion that NEITHER RemoveFederationPeer nor RemoveRemote
// was called proves the origin remote is never touched. Reverting the
// ValidatePeerName call at the top of runFederationRemovePeer makes this RED —
// the fake would record a clobbering RemoveFederationPeer call.
func TestFederationRemovePeerRejectsReservedOrigin(t *testing.T) {
	cases := []struct {
		name string
		peer string
	}{
		{name: "plain lowercase", peer: "origin"},
		{name: "plain uppercase", peer: "ORIGIN"},
		{name: "plain mixed case", peer: "Origin"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fake := &federationAddPeerFakeStore{}

			prevStore, prevActive, prevJSON, prevProxied := store, storeActive, jsonOutput, proxiedServerMode
			store = fake
			storeActive = true
			jsonOutput = false
			proxiedServerMode = false
			t.Cleanup(func() {
				store, storeActive, jsonOutput, proxiedServerMode = prevStore, prevActive, prevJSON, prevProxied
			})

			// HandleError writes the human message to stderr and returns a
			// sentinel exitError, so capture stderr to assert the reason.
			origStderr := os.Stderr
			r, w, pipeErr := os.Pipe()
			if pipeErr != nil {
				t.Fatalf("os.Pipe: %v", pipeErr)
			}
			os.Stderr = w

			err := runFederationRemovePeer(federationRemovePeerCmd, []string{tc.peer})

			w.Close()
			os.Stderr = origStderr
			stderrOut, _ := io.ReadAll(r)

			if err == nil {
				t.Fatalf("remove-peer %q must be rejected (reserved name), got nil error", tc.peer)
			}
			if !strings.Contains(string(stderrOut), "reserved") {
				t.Errorf("stderr should explain the name is reserved; got: %q", string(stderrOut))
			}

			// The clobber can only happen through a store write. Neither the
			// credential-row delete nor the best-effort remote removal must run.
			if len(fake.removeFederationPeerCalls) != 0 {
				t.Errorf("reserved-name remove-peer must NOT call RemoveFederationPeer (removes backing origin remote); got %v", fake.removeFederationPeerCalls)
			}
			if len(fake.removeRemoteCalls) != 0 {
				t.Errorf("reserved-name remove-peer must NOT call RemoveRemote (would delete origin); got %v", fake.removeRemoteCalls)
			}
		})
	}
}
