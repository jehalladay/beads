package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// federationAddPeerFakeStore records which storage method `bd federation
// add-peer` dispatches to, and captures the FederationPeer passed to
// AddFederationPeer. It embeds storage.DoltStorage (nil) so it satisfies the
// full interface; only the two add methods are overridden.
type federationAddPeerFakeStore struct {
	storage.DoltStorage
	addRemoteCalls         []string
	addFederationPeerPeers []*storage.FederationPeer
}

func (f *federationAddPeerFakeStore) AddRemote(_ context.Context, name, _ string) error {
	f.addRemoteCalls = append(f.addRemoteCalls, name)
	return nil
}

func (f *federationAddPeerFakeStore) AddFederationPeer(_ context.Context, peer *storage.FederationPeer) error {
	f.addFederationPeerPeers = append(f.addFederationPeerPeers, peer)
	return nil
}

// TestFederationAddPeerSovereigntyPersists guards beads-pib1g: `bd federation
// add-peer <name> <url> --sovereignty T2` WITHOUT --user must persist the
// sovereignty tier. Only store.AddFederationPeer writes the federation_peers
// row (which carries the `sovereignty` column, schema 0015); store.AddRemote
// only creates a Dolt remote and drops the tier. The command echoes the tier
// back in both text and --json output, so routing to AddRemote made add-peer
// report a tier it silently discarded (read-after-write-emits-unpersisted-value
// class). The fix routes through AddFederationPeer whenever --user OR
// --sovereignty is set — the symmetric-inverse of beads-af5te (remove-peer).
//
// This is a load-bearing dispatch test: the bug IS the branch selection, so
// asserting the method dispatched to (and that the peer carries Sovereignty)
// goes RED if the else-if reverts to the AddRemote branch — a pure output test
// of the helper would not catch it.
func TestFederationAddPeerSovereigntyPersists(t *testing.T) {
	fake := &federationAddPeerFakeStore{}

	prevStore, prevActive, prevJSON, prevProxied := store, storeActive, jsonOutput, proxiedServerMode
	prevUser, prevPassword, prevSov := federationUser, federationPassword, federationSov
	store = fake
	storeActive = true
	jsonOutput = false
	proxiedServerMode = false
	// Sovereignty tier set, NO user — the previously-dropping case.
	federationUser = ""
	federationPassword = ""
	federationSov = "T2"
	t.Cleanup(func() {
		store, storeActive, jsonOutput, proxiedServerMode = prevStore, prevActive, prevJSON, prevProxied
		federationUser, federationPassword, federationSov = prevUser, prevPassword, prevSov
	})

	if err := runFederationAddPeer(federationAddPeerCmd, []string{"sov-peer", "file:///tmp/sovhub"}); err != nil {
		t.Fatalf("runFederationAddPeer: %v", err)
	}

	// It must persist via AddFederationPeer (the only path that writes the
	// sovereignty column), carrying the tier.
	if len(fake.addFederationPeerPeers) != 1 {
		t.Fatalf("add-peer --sovereignty must call AddFederationPeer once to persist the tier; got %d calls (addRemote calls: %v)",
			len(fake.addFederationPeerPeers), fake.addRemoteCalls)
	}
	got := fake.addFederationPeerPeers[0]
	if got.Sovereignty != "T2" {
		t.Errorf("AddFederationPeer must carry the sovereignty tier; got Sovereignty=%q", got.Sovereignty)
	}
	if got.Name != "sov-peer" || got.RemoteURL != "file:///tmp/sovhub" {
		t.Errorf("AddFederationPeer peer name/url mismatch; got Name=%q RemoteURL=%q", got.Name, got.RemoteURL)
	}

	// It must NOT take the plain AddRemote branch, which drops the tier.
	if len(fake.addRemoteCalls) != 0 {
		t.Errorf("add-peer --sovereignty must not call AddRemote directly (drops the tier); got %v",
			fake.addRemoteCalls)
	}
}

// TestFederationAddPeerNoSovereigntyNoAuthUsesAddRemote pins the unchanged
// behaviour: with neither --user nor --sovereignty, add-peer stays on the
// lightweight AddRemote path (no federation_peers credential row created).
func TestFederationAddPeerNoSovereigntyNoAuthUsesAddRemote(t *testing.T) {
	fake := &federationAddPeerFakeStore{}

	prevStore, prevActive, prevJSON, prevProxied := store, storeActive, jsonOutput, proxiedServerMode
	prevUser, prevPassword, prevSov := federationUser, federationPassword, federationSov
	store = fake
	storeActive = true
	jsonOutput = false
	proxiedServerMode = false
	federationUser = ""
	federationPassword = ""
	federationSov = ""
	t.Cleanup(func() {
		store, storeActive, jsonOutput, proxiedServerMode = prevStore, prevActive, prevJSON, prevProxied
		federationUser, federationPassword, federationSov = prevUser, prevPassword, prevSov
	})

	if err := runFederationAddPeer(federationAddPeerCmd, []string{"plain-peer", "file:///tmp/plainhub"}); err != nil {
		t.Fatalf("runFederationAddPeer: %v", err)
	}

	if len(fake.addRemoteCalls) != 1 || fake.addRemoteCalls[0] != "plain-peer" {
		t.Errorf("plain add-peer (no user, no sovereignty) must call AddRemote once; got %v", fake.addRemoteCalls)
	}
	if len(fake.addFederationPeerPeers) != 0 {
		t.Errorf("plain add-peer must not create a federation_peers credential row; got %d AddFederationPeer calls",
			len(fake.addFederationPeerPeers))
	}
}
