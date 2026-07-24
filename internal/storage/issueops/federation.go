package issueops

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
)

// validPeerNameRegex matches valid peer names (alphanumeric, hyphens, underscores).
var validPeerNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// reservedPeerName is the Dolt remote name bd itself owns for the local↔remote
// git-backed issue-sync path (bd dolt push/pull target dolt_remotes.origin).
// A federation peer named "origin" would route through AddFederationPeer ->
// UpsertRemote / AddRemote and CLOBBER that backing remote (remove+re-add at
// the peer's URL), silently misdirecting the primary sync path — and beads-j785d
// then hides such a peer from status/list-peers/sync, making it un-diagnosable.
const reservedPeerName = "origin"

// ValidatePeerName checks that a peer name is safe for use as a Dolt remote name.
func ValidatePeerName(name string) error {
	if name == "" {
		return fmt.Errorf("peer name cannot be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("peer name too long (max 64 characters)")
	}
	if !validPeerNameRegex.MatchString(name) {
		return fmt.Errorf("peer name must start with a letter and contain only alphanumeric characters, hyphens, and underscores")
	}
	if strings.EqualFold(name, reservedPeerName) {
		return fmt.Errorf("peer name %q is reserved for the backing git remote and cannot be used for a federation peer", name)
	}
	return nil
}

// AddFederationPeerInTx upserts a federation peer record. The encryptedPwd
// should already be encrypted by the caller; pass nil for no password.
func AddFederationPeerInTx(ctx context.Context, tx *sql.Tx, peer *storage.FederationPeer, encryptedPwd []byte) error {
	if err := ValidatePeerName(peer.Name); err != nil {
		return fmt.Errorf("invalid peer name: %w", err)
	}

	// beads-9mg09: COALESCE the credential pair on a re-add (ON DUPLICATE KEY
	// UPDATE) instead of full-replacing it. add-peer has full-replace upsert
	// semantics but a partial-flag UX and no separate update-peer verb, so a
	// re-add that OMITS --user (e.g. only to change --sovereignty or the URL)
	// arrives here with Username:"" / encryptedPwd:nil and previously WIPED the
	// stored credentials. They are live-consumed — withPeerCredentials
	// (dolt/credentials.go) reads them on EVERY authenticated federation op and
	// only authenticates when they are non-empty — so the wipe silently downgraded
	// an authenticated peer to credential-free (fail-open on a security boundary),
	// with no warning.
	//
	// username+password are treated as ONE UNIT keyed on username presence: the
	// CLI only ever stores a password alongside a username (it prompts for one when
	// --user is given), so an empty incoming username means "credentials not
	// supplied on this re-add" → preserve BOTH stored fields. Gating each field
	// independently could keep a NEW username while preserving the OLD user's
	// password (a cross-credential mismatch). Clearing credentials is done
	// deliberately via remove-peer, not a bare re-add.
	//
	// beads-x7ias: COALESCE sovereignty on re-add too — an empty incoming
	// sovereignty preserves the stored tier. --sovereignty is an OPTIONAL flag
	// (default ""), so a re-add that changes only --user/--password or the URL
	// arrives with sovereignty="" and previously WIPED a set T1/T2/T3/T4 tier
	// silently. Unlike remote_url (a MANDATORY positional arg — never
	// accidentally empty), sovereignty IS a governance/policy boundary
	// (see beads-9l21w) that is accidentally empty on any re-add not repeating
	// the flag, so full-replace loses it. Clearing a tier is done deliberately
	// (remove-peer, or a future explicit tier-clear), not a bare re-add.
	// remote_url stays full-replace (beads-s5sue also reconciles the Dolt
	// remote to it): the caller always supplies it.
	_, err := tx.ExecContext(ctx, `
		INSERT INTO federation_peers (name, remote_url, username, password_encrypted, sovereignty)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			remote_url = VALUES(remote_url),
			username = IF(VALUES(username) = '', username, VALUES(username)),
			password_encrypted = IF(VALUES(username) = '', password_encrypted, VALUES(password_encrypted)),
			sovereignty = IF(VALUES(sovereignty) = '', sovereignty, VALUES(sovereignty)),
			updated_at = CURRENT_TIMESTAMP
	`, peer.Name, peer.RemoteURL, peer.Username, encryptedPwd, peer.Sovereignty)

	if err != nil {
		return fmt.Errorf("add federation peer: %w", err)
	}
	return nil
}

// FederationPeerRow holds raw database fields for a federation peer.
// The caller is responsible for decrypting EncryptedPwd.
type FederationPeerRow struct {
	Peer         storage.FederationPeer
	EncryptedPwd []byte
}

// GetFederationPeerInTx retrieves a federation peer by name.
// Returns storage.ErrNotFound (wrapped) if the peer does not exist.
func GetFederationPeerInTx(ctx context.Context, tx *sql.Tx, name string) (*FederationPeerRow, error) {
	var row FederationPeerRow
	var lastSync sql.NullTime
	var username sql.NullString

	err := tx.QueryRowContext(ctx, `
		SELECT name, remote_url, username, password_encrypted, sovereignty, last_sync, created_at, updated_at
		FROM federation_peers WHERE name = ?
	`, name).Scan(
		&row.Peer.Name, &row.Peer.RemoteURL, &username, &row.EncryptedPwd,
		&row.Peer.Sovereignty, &lastSync, &row.Peer.CreatedAt, &row.Peer.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: federation peer %s", storage.ErrNotFound, name)
	}
	if err != nil {
		return nil, fmt.Errorf("get federation peer: %w", err)
	}

	if username.Valid {
		row.Peer.Username = username.String
	}
	if lastSync.Valid {
		row.Peer.LastSync = &lastSync.Time
	}

	return &row, nil
}

// ListFederationPeersInTx returns all configured federation peer rows.
func ListFederationPeersInTx(ctx context.Context, tx *sql.Tx) ([]*FederationPeerRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT name, remote_url, username, password_encrypted, sovereignty, last_sync, created_at, updated_at
		FROM federation_peers ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list federation peers: %w", err)
	}
	defer rows.Close()

	var peers []*FederationPeerRow
	for rows.Next() {
		var row FederationPeerRow
		var lastSync sql.NullTime
		var username sql.NullString

		if err := rows.Scan(
			&row.Peer.Name, &row.Peer.RemoteURL, &username, &row.EncryptedPwd,
			&row.Peer.Sovereignty, &lastSync, &row.Peer.CreatedAt, &row.Peer.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan federation peer: %w", err)
		}

		if username.Valid {
			row.Peer.Username = username.String
		}
		if lastSync.Valid {
			row.Peer.LastSync = &lastSync.Time
		}

		peers = append(peers, &row)
	}
	return peers, rows.Err()
}

// RemoveFederationPeerInTx deletes a federation peer by name.
func RemoveFederationPeerInTx(ctx context.Context, tx *sql.Tx, name string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM federation_peers WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("remove federation peer: %w", err)
	}
	return nil
}

// AddRemoteIfNotExists adds a Dolt remote, ignoring "already exists" errors.
// This is a helper used when adding federation peers that also need a Dolt remote.
func AddRemoteIfNotExists(ctx context.Context, tx *sql.Tx, name, url string) error {
	_, err := tx.ExecContext(ctx, "CALL DOLT_REMOTE('add', ?, ?)", name, url)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("add remote %s: %w", name, err)
	}
	return nil
}

// UpsertRemote makes the Dolt remote named `name` authoritative on `url`. Unlike
// AddRemoteIfNotExists (which swallows "already exists" and leaves a preexisting
// remote pointing at its OLD url), this reconciles a URL change on re-add.
//
// beads-s5sue: add-peer has full-replace column semantics but no update-peer
// verb, so a legitimate re-add to change the URL updates federation_peers.remote_url
// (VALUES(remote_url) in AddFederationPeerInTx) while the OLD Dolt remote — which
// is what sync/push/pull actually target (they read dolt_remotes via ListRemotes,
// not the column) — silently kept its stale url. The add-peer command echoed
// "Added peer …: <new-url>" so the operator believed the re-point succeeded while
// federation kept hitting the old remote: a silent misdirected-data-path plus a
// permanent remote_url-column-vs-Dolt-remote divergence.
//
// Fix: read the existing remote's url; if it matches, no-op (idempotent re-add);
// if it differs, remove and re-add so the Dolt remote matches the requested url
// atomically inside the caller's tx. Peer identity (name) is unchanged, so the
// remote-tracking refs are re-established on the next sync (same as a fresh peer).
func UpsertRemote(ctx context.Context, tx *sql.Tx, name, url string) error {
	var existingURL string
	err := tx.QueryRowContext(ctx, "SELECT url FROM dolt_remotes WHERE name = ?", name).Scan(&existingURL)
	switch {
	case err == sql.ErrNoRows:
		// No remote yet — plain add.
		return AddRemoteIfNotExists(ctx, tx, name, url)
	case err != nil:
		return fmt.Errorf("look up remote %s: %w", name, err)
	}

	if existingURL == url {
		// Already points at the requested url — nothing to do.
		return nil
	}

	// URL changed: reconcile the Dolt remote to the requested url. Remove then
	// re-add (Dolt exposes no set-url primitive), all within the caller's tx so
	// a failure rolls back alongside the credential/column write.
	if _, err := tx.ExecContext(ctx, "CALL DOLT_REMOTE('remove', ?)", name); err != nil {
		return fmt.Errorf("remove stale remote %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, "CALL DOLT_REMOTE('add', ?, ?)", name, url); err != nil {
		return fmt.Errorf("re-add remote %s at new url: %w", name, err)
	}
	return nil
}
