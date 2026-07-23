package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// TestAddFederationPeerReAddReconcilesRemoteURL (beads-s5sue) proves that a
// re-add of an existing peer with a CHANGED URL updates the Dolt remote — not
// just the federation_peers.remote_url column.
//
// add-peer has full-replace column semantics but no separate update-peer verb,
// so a legitimate re-add to re-point a peer at a new remote updates the
// federation_peers.remote_url column (VALUES(remote_url) in
// AddFederationPeerInTx). But the Dolt remote — which is what sync/push/pull
// actually target (they read dolt_remotes via ListRemotes, NOT the column) —
// was added via AddRemoteIfNotExists, which SWALLOWS "already exists" and left
// the OLD remote pointing at the STALE url. So the operator saw "Added peer …:
// <new-url>" and the column agreed, but federation kept hitting the old remote:
// a silent misdirected-data-path plus a permanent column-vs-Dolt-remote divergence.
//
// The fix routes both stores through issueops.UpsertRemote, which reconciles the
// Dolt remote to the requested url (remove + re-add on change) inside the same tx.
//
// This test reads the ACTUAL Dolt remote (store.ListRemotes → dolt_remotes) so it
// catches the divergence the column read would hide.
//
// MUTATION-VERIFIED: revert the AddFederationPeer calls back to
// issueops.AddRemoteIfNotExists (or make UpsertRemote a no-op on change) and the
// post-re-add ListRemotes still reports the ORIGINAL url → RED.
func TestAddFederationPeerReAddReconcilesRemoteURL(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	store.beadsDir = t.TempDir()
	store.credentialKey = nil

	const name = "urlpeer"
	const urlV1 = "file:///tmp/beads-s5sue-remote-v1"
	const urlV2 = "file:///tmp/beads-s5sue-remote-v2"

	// 1. Add the peer at the original URL (sovereignty-carrying so the row is a
	//    federation_peers row, mirroring the real CLI routing).
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name:        name,
		RemoteURL:   urlV1,
		Sovereignty: "T2",
	}); err != nil {
		t.Fatalf("initial AddFederationPeer: %v", err)
	}
	if got := remoteURL(t, store, ctx, name); got != urlV1 {
		t.Fatalf("precondition: Dolt remote url after first add = %q, want %q", got, urlV1)
	}

	// 2. Re-add the SAME peer at a DIFFERENT URL (the re-point).
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name:        name,
		RemoteURL:   urlV2,
		Sovereignty: "T2",
	}); err != nil {
		t.Fatalf("url-changing re-add: %v", err)
	}

	// 3. The Dolt remote (what sync/push/pull target) MUST now point at the new url.
	if got := remoteURL(t, store, ctx, name); got != urlV2 {
		t.Errorf("REGRESSION (beads-s5sue): re-add changed the column but the Dolt remote kept the stale url (got %q, want %q) — sync would silently target the old remote", got, urlV2)
	}

	// 4. The federation_peers.remote_url column must agree (no divergence).
	peer, err := store.GetFederationPeer(ctx, name)
	if err != nil {
		t.Fatalf("GetFederationPeer after re-add: %v", err)
	}
	if peer.RemoteURL != urlV2 {
		t.Errorf("federation_peers.remote_url = %q, want %q (column-vs-remote divergence)", peer.RemoteURL, urlV2)
	}

	// 5. Idempotent re-add at the SAME (new) url must not error or duplicate.
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name:        name,
		RemoteURL:   urlV2,
		Sovereignty: "T2",
	}); err != nil {
		t.Fatalf("idempotent re-add at unchanged url: %v", err)
	}
	if got := remoteURL(t, store, ctx, name); got != urlV2 {
		t.Errorf("idempotent re-add perturbed the remote url (got %q, want %q)", got, urlV2)
	}
}

// remoteURL returns the URL of the named Dolt remote (dolt_remotes), or "" if
// absent. This is the source of truth sync/push/pull use — deliberately NOT the
// federation_peers.remote_url column.
func remoteURL(t *testing.T, store *DoltStore, ctx context.Context, name string) string {
	t.Helper()
	remotes, err := store.ListRemotes(ctx)
	if err != nil {
		t.Fatalf("ListRemotes: %v", err)
	}
	for _, r := range remotes {
		if r.Name == name {
			return r.URL
		}
	}
	return ""
}
