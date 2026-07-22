package dolt

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// TestAddFederationPeerAtomicOnRemoteFailure (beads-1170j) proves that
// AddFederationPeer is atomic on the DoltStore (server) path: if the Dolt
// remote cannot be added (e.g. a malformed remote URL that DOLT_REMOTE
// rejects with a URL-parse error, which is NOT "already exists"), the
// credential row must NOT be left behind in federation_peers.
//
// The buggy pre-fix code ran the INSERT via execContext (which auto-commits
// its own implicit transaction) and only THEN called AddRemote. When AddRemote
// failed, the credential row was already committed → an orphaned peer record
// with no matching Dolt remote. The fix wraps both writes in a single SQL
// transaction (mirroring the EmbeddedDoltStore path) so the INSERT rolls back
// when the remote add fails.
//
// Uses an empty-password peer so the test isolates the atomicity concern from
// credential-key encryption (no beadsDir key init needed); the INSERT +
// AddRemote-in-one-tx path is exercised identically regardless of password.
func TestAddFederationPeerAtomicOnRemoteFailure(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// A malformed remote URL: DOLT_REMOTE('add', ...) rejects this with a
	// URL-parse error ("first path segment in URL cannot contain colon"),
	// which is deterministic and is NOT the ignored "already exists" case.
	peer := &storage.FederationPeer{
		Name:        "atomicpeer",
		RemoteURL:   "ht!tp://::bad",
		Username:    "alice",
		Sovereignty: "T2",
	}

	// AddFederationPeer must fail because the remote cannot be added.
	if err := store.AddFederationPeer(ctx, peer); err == nil {
		t.Fatal("AddFederationPeer() expected error on malformed remote URL, got nil")
	}

	// The credential row must NOT survive the failed add — the whole operation
	// is atomic. On the buggy code the execContext INSERT auto-committed before
	// AddRemote failed, orphaning the row here.
	if _, err := store.GetFederationPeer(ctx, peer.Name); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GetFederationPeer(%q) after failed add: expected ErrNotFound (atomic rollback), got err=%v", peer.Name, err)
	}
}
