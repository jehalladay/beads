package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// TestAddFederationPeerReAddPreservesSovereignty (beads-x7ias) proves that a
// re-add of an existing peer that OMITS --sovereignty does not WIPE the stored
// governance tier.
//
// add-peer has full-replace upsert semantics but a partial-flag UX and no
// separate update-peer verb, so a legitimate re-add to change only the
// credentials (or URL) arrives at the shared seam
// (issueops.AddFederationPeerInTx) with Sovereignty:"" (--sovereignty is an
// OPTIONAL flag defaulting to ""). The buggy upsert
// (`sovereignty = VALUES(sovereignty)`) then overwrote the stored tier with an
// empty value — silently WIPING a set T1/T2/T3/T4 policy tier. Sibling of
// beads-9mg09 (credential COALESCE): 9mg09 deliberately kept sovereignty
// full-replace as "not security-sensitive", but that rationale holds only for
// remote_url (a MANDATORY positional arg, never accidentally empty) and breaks
// for sovereignty (an OPTIONAL flag that IS a governance boundary — see
// beads-9l21w — and is accidentally empty on any re-add that does not repeat
// it). The fix COALESCEs sovereignty on the upsert.
//
// Uses a real DoltStore (setupTestStore) so the ON DUPLICATE KEY UPDATE SQL is
// validated for real.
//
// MUTATION-VERIFIED: revert AddFederationPeerInTx's upsert leg back to
// `sovereignty = VALUES(sovereignty)` and this test goes RED (the re-add wipes
// the tier to "").
func TestAddFederationPeerReAddPreservesSovereignty(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	store.beadsDir = t.TempDir()
	store.credentialKey = nil

	const name = "sovpeer"
	const url = "file:///tmp/beads-x7ias-fed-sov"

	// 1. Add the peer WITH a governance tier (the state the wipe erased).
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name:        name,
		RemoteURL:   url,
		Sovereignty: "T2",
	}); err != nil {
		t.Fatalf("initial AddFederationPeer with sovereignty: %v", err)
	}

	// 2. Re-add the SAME peer to change ONLY the credentials — no --sovereignty,
	//    exactly as the CLI presents it when omitted (empty string).
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name:        name,
		RemoteURL:   url,
		Username:    "alice",
		Sovereignty: "",
	}); err != nil {
		t.Fatalf("sovereignty-omitting re-add: %v", err)
	}

	// 3. The stored governance tier must SURVIVE the re-add.
	got, err := store.GetFederationPeer(ctx, name)
	if err != nil {
		t.Fatalf("GetFederationPeer after re-add: %v", err)
	}
	if got.Sovereignty != "T2" {
		t.Errorf("REGRESSION (beads-x7ias): re-add without --sovereignty WIPED the tier (got %q, want %q)", got.Sovereignty, "T2")
	}
	// 4. The intended change (the username) must still take effect — the COALESCE
	//    protects the tier WITHOUT freezing the fields the re-add meant to update.
	if got.Username != "alice" {
		t.Errorf("re-add did not apply the credential change (got %q, want %q)", got.Username, "alice")
	}

	// 5. An EXPLICIT tier change on re-add must still take effect (COALESCE only
	//    protects the OMITTED case, it does not freeze the field).
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name:        name,
		RemoteURL:   url,
		Sovereignty: "T4",
	}); err != nil {
		t.Fatalf("explicit sovereignty re-add: %v", err)
	}
	got, err = store.GetFederationPeer(ctx, name)
	if err != nil {
		t.Fatalf("GetFederationPeer after explicit tier change: %v", err)
	}
	if got.Sovereignty != "T4" {
		t.Errorf("explicit --sovereignty re-add did not apply (got %q, want %q) — COALESCE must not freeze the field", got.Sovereignty, "T4")
	}
}
