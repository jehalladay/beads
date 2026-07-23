package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// TestAddFederationPeerReAddPreservesCredentials (beads-9mg09) proves that a
// re-add of an existing peer that OMITS credentials does not WIPE the stored
// ones.
//
// add-peer has full-replace upsert semantics but a partial-flag UX and no
// separate update-peer verb, so a legitimate re-add to change only the
// sovereignty (or URL) arrives at the shared seam (issueops.AddFederationPeerInTx)
// with Username:"" / encryptedPwd:nil. The buggy upsert
// (`username = VALUES(username), password_encrypted = VALUES(password_encrypted)`)
// then overwrote the live stored credentials with empty values — silently
// downgrading an authenticated peer to credential-free (fail-open on a security
// boundary, since withPeerCredentials only authenticates when they are
// non-empty). The fix COALESCEs the credential pair on the upsert, keyed on
// username presence.
//
// Uses a real DoltStore (setupTestStore) so the ON DUPLICATE KEY UPDATE SQL is
// validated for real, and a password peer (with an isolated credential key) so
// the round-trip through GetFederationPeer proves the ENCRYPTED password
// survived, not just the username.
//
// MUTATION-VERIFIED: revert AddFederationPeerInTx's upsert back to
// `username = VALUES(username), password_encrypted = VALUES(password_encrypted)`
// and this test goes RED (the re-add wipes both fields).
func TestAddFederationPeerReAddPreservesCredentials(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Force the fresh-credential-key path onto an isolated temp dir so the
	// password actually encrypts (mirrors TestAddFederationPeerWithPasswordDoesNotDeadlock).
	store.beadsDir = t.TempDir()
	store.credentialKey = nil

	const name = "credpeer"

	// 1. Add the peer WITH credentials (the authenticated state the wipe erased).
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name:        name,
		RemoteURL:   "file:///tmp/beads-9mg09-fed-original",
		Username:    "alice",
		Password:    "s3cr3t-federation-token",
		Sovereignty: "T2",
	}); err != nil {
		t.Fatalf("initial AddFederationPeer with credentials: %v", err)
	}

	// 2. Re-add the SAME peer to change ONLY the sovereignty — no --user/--password,
	//    exactly as the CLI presents them when omitted (empty strings).
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name:        name,
		RemoteURL:   "file:///tmp/beads-9mg09-fed-original",
		Username:    "",
		Password:    "",
		Sovereignty: "T3",
	}); err != nil {
		t.Fatalf("credential-omitting re-add: %v", err)
	}

	// 3. The stored credentials must SURVIVE the re-add.
	got, err := store.GetFederationPeer(ctx, name)
	if err != nil {
		t.Fatalf("GetFederationPeer after re-add: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("REGRESSION (beads-9mg09): re-add without --user WIPED the username (got %q, want %q)", got.Username, "alice")
	}
	if got.Password != "s3cr3t-federation-token" {
		t.Errorf("REGRESSION (beads-9mg09): re-add without --password WIPED the stored password (got %q, want %q)", got.Password, "s3cr3t-federation-token")
	}
	// 4. The intended change (sovereignty) must still take effect — the COALESCE
	//    protects credentials WITHOUT freezing the fields the re-add meant to update.
	if got.Sovereignty != "T3" {
		t.Errorf("re-add did not apply the sovereignty change (got %q, want %q)", got.Sovereignty, "T3")
	}
}
