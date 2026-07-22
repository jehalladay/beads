package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// federationRemovePeerFakeStore records which storage method `bd federation
// remove-peer` dispatches to. It embeds storage.DoltStorage (nil) so it
// satisfies the full interface; only the two remove methods are overridden.
type federationRemovePeerFakeStore struct {
	storage.DoltStorage
	removeRemoteCalls         []string
	removeFederationPeerCalls []string
}

func (f *federationRemovePeerFakeStore) RemoveRemote(_ context.Context, name string) error {
	f.removeRemoteCalls = append(f.removeRemoteCalls, name)
	return nil
}

func (f *federationRemovePeerFakeStore) RemoveFederationPeer(_ context.Context, name string) error {
	f.removeFederationPeerCalls = append(f.removeFederationPeerCalls, name)
	return nil
}

// TestFederationRemovePeerCallsRemoveFederationPeer guards beads-af5te:
// `bd federation remove-peer` must dispatch to store.RemoveFederationPeer
// (which DELETEs the federation_peers credential row AND removes the Dolt
// remote), NOT store.RemoveRemote (which only touches the Dolt remote). The
// asymmetric wiring (add-peer --user writes federation_peers + remote, but
// remove-peer only removed the remote) orphaned the encrypted password_encrypted
// row forever — and it's invisible to list-peers/status (both read ListRemotes,
// not federation_peers). RemoveFederationPeer removes the remote best-effort
// itself, so calling it is the complete, symmetric inverse of add-peer.
func TestFederationRemovePeerCallsRemoveFederationPeer(t *testing.T) {
	fake := &federationRemovePeerFakeStore{}

	prevStore, prevActive, prevJSON, prevProxied := store, storeActive, jsonOutput, proxiedServerMode
	store = fake
	storeActive = true
	jsonOutput = false
	proxiedServerMode = false
	t.Cleanup(func() {
		store, storeActive, jsonOutput, proxiedServerMode = prevStore, prevActive, prevJSON, prevProxied
	})

	if err := runFederationRemovePeer(federationRemovePeerCmd, []string{"cred-peer"}); err != nil {
		t.Fatalf("runFederationRemovePeer: %v", err)
	}

	if len(fake.removeFederationPeerCalls) != 1 || fake.removeFederationPeerCalls[0] != "cred-peer" {
		t.Errorf("remove-peer must call RemoveFederationPeer(\"cred-peer\") once to delete the credential row; got %v",
			fake.removeFederationPeerCalls)
	}
	// It must NOT bypass the credential-deleting method by calling RemoveRemote
	// directly (that leaves the federation_peers row orphaned). RemoveFederationPeer
	// handles the remote removal internally, so the cmd layer never calls RemoveRemote.
	if len(fake.removeRemoteCalls) != 0 {
		t.Errorf("remove-peer must not call RemoveRemote directly (orphans the credential row); got %v",
			fake.removeRemoteCalls)
	}
}
