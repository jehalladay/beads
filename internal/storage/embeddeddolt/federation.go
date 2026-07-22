//go:build cgo

package embeddeddolt

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
)

// credentialKeyFile is the filename for the random encryption key.
const credentialKeyFile = ".beads-credential-key" //nolint:gosec // G101: filename, not a credential

// federationStagingBranch is the temporary branch used to filter excluded
// issue types before pushing to a federation peer. It mirrors the server
// DoltStore's staging branch (dolt/federation.go) so both backends filter the
// same way (beads-t129).
const federationStagingBranch = "__federation_push_staging"

// ensureCredentialKey lazily initializes the credential encryption key.
func (s *EmbeddedDoltStore) ensureCredentialKey() error {
	if s.credentialKey != nil {
		return nil
	}
	if s.beadsDir == "" {
		return fmt.Errorf("beads directory not set; credential encryption unavailable")
	}

	keyPath := filepath.Join(s.beadsDir, credentialKeyFile)

	// Try to load existing key.
	key, err := os.ReadFile(keyPath) //nolint:gosec // G304: keyPath derived from trusted beadsDir
	if err == nil && len(key) == 32 {
		s.credentialKey = key
		return nil
	}

	// Generate new random 32-byte key (AES-256).
	key = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return fmt.Errorf("generate credential key: %w", err)
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return fmt.Errorf("write credential key: %w", err)
	}

	s.credentialKey = key
	return nil
}

func (s *EmbeddedDoltStore) encryptPassword(password string) ([]byte, error) {
	if password == "" {
		return nil, nil
	}
	if err := s.ensureCredentialKey(); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.credentialKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(password), nil), nil
}

func (s *EmbeddedDoltStore) decryptPassword(encrypted []byte) (string, error) {
	if len(encrypted) == 0 {
		return "", nil
	}
	if err := s.ensureCredentialKey(); err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.credentialKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(encrypted) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := encrypted[:nonceSize], encrypted[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// ---------------------------------------------------------------------------
// FederationStore implementation
// ---------------------------------------------------------------------------

func (s *EmbeddedDoltStore) AddFederationPeer(ctx context.Context, peer *storage.FederationPeer) error {
	encryptedPwd, err := s.encryptPassword(peer.Password)
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}

	if err := s.withConn(ctx, true, func(tx *sql.Tx) error {
		if err := issueops.AddFederationPeerInTx(ctx, tx, peer, encryptedPwd); err != nil {
			return err
		}
		// Also add the Dolt remote.
		return issueops.AddRemoteIfNotExists(ctx, tx, peer.Name, peer.RemoteURL)
	}); err != nil {
		return err
	}
	return nil
}

func (s *EmbeddedDoltStore) GetFederationPeer(ctx context.Context, name string) (*storage.FederationPeer, error) {
	var row *issueops.FederationPeerRow
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		row, err = issueops.GetFederationPeerInTx(ctx, tx, name)
		return err
	})
	if err != nil {
		return nil, err
	}

	if len(row.EncryptedPwd) > 0 {
		row.Peer.Password, err = s.decryptPassword(row.EncryptedPwd)
		if err != nil {
			return nil, fmt.Errorf("decrypt password: %w", err)
		}
	}
	return &row.Peer, nil
}

func (s *EmbeddedDoltStore) ListFederationPeers(ctx context.Context) ([]*storage.FederationPeer, error) {
	var rows []*issueops.FederationPeerRow
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		rows, err = issueops.ListFederationPeersInTx(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}

	peers := make([]*storage.FederationPeer, 0, len(rows))
	for _, row := range rows {
		if len(row.EncryptedPwd) > 0 {
			pwd, err := s.decryptPassword(row.EncryptedPwd)
			if err != nil {
				return nil, fmt.Errorf("decrypt password for peer %s: %w", row.Peer.Name, err)
			}
			row.Peer.Password = pwd
		}
		peers = append(peers, &row.Peer)
	}
	return peers, nil
}

func (s *EmbeddedDoltStore) RemoveFederationPeer(ctx context.Context, name string) error {
	if err := s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.RemoveFederationPeerInTx(ctx, tx, name)
	}); err != nil {
		return err
	}

	// Also remove the Dolt remote (best-effort).
	if rmErr := s.RemoveRemote(ctx, name); rmErr != nil {
		if !strings.Contains(rmErr.Error(), "not found") {
			// Silently ignore "not found" — the remote may not exist.
			_ = rmErr
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// SyncStore implementation
// ---------------------------------------------------------------------------

// Sync performs a full bidirectional sync with a peer:
// 1. Fetch from peer
// 2. Merge peer's changes (handling conflicts per strategy)
// 3. Push local changes to peer
func (s *EmbeddedDoltStore) Sync(ctx context.Context, peer string, strategy string) (*storage.SyncResult, error) {
	result := &storage.SyncResult{
		Peer:      peer,
		StartTime: time.Now(),
	}

	// GH#2474 / bd-578h9.2: commit pending changes before the merge, matching
	// embedded Pull/PullRemote/PullFrom and server-mode Sync. Embedded Commit is
	// DOLT_COMMIT('-Am'), so it stages config — where kv.memory.* memories live —
	// and a leftover dirty working set (e.g. a `bd remember` write) would
	// otherwise make DOLT_MERGE refuse to start ("cannot merge with uncommitted
	// changes"). CommitPending is a no-op when the working set is already clean.
	if _, err := s.CommitPending(ctx, "beads"); err != nil {
		result.Error = fmt.Errorf("commit pending before sync: %w", err)
		return result, result.Error
	}

	// Step 1: Fetch
	if err := s.Fetch(ctx, peer); err != nil {
		result.Error = fmt.Errorf("fetch failed: %w", err)
		return result, result.Error
	}
	result.Fetched = true

	// Step 2: Get commit before merge for change detection
	beforeCommit, _ := s.GetCurrentCommit(ctx)

	// Step 3: Merge peer's branch
	remoteBranch := fmt.Sprintf("%s/%s", peer, s.branch)
	bootstrap := false
	conflicts, err := s.Merge(ctx, remoteBranch)
	if err != nil {
		// beads-aapwu: a fresh peer added via `bd federation add-peer` has no
		// branch yet, so the remote-tracking ref peer/main does not exist and
		// DOLT_MERGE fails "branch not found: peer/main". That is not a fatal
		// error — it is the bootstrap case: there is nothing to merge yet, and
		// Step 5's push below is exactly what publishes the local town and
		// creates the peer's branch. Skip the merge (leave Merged=false, no
		// pulled commits) and fall through to the publishing push, so the
		// documented add-peer -> sync onboarding works against an empty peer.
		if !versioncontrolops.IsBranchNotFoundError(err) {
			result.Error = fmt.Errorf("merge failed: %w", err)
			return result, result.Error
		}
		bootstrap = true
		conflicts = nil
	}

	// Step 4: Handle conflicts
	if len(conflicts) > 0 {
		result.Conflicts = conflicts

		if strategy == "" {
			result.Error = fmt.Errorf("merge conflicts require resolution (use --strategy ours|theirs)")
			return result, result.Error
		}

		for _, c := range conflicts {
			if err := s.ResolveConflicts(ctx, c.Field, strategy); err != nil {
				result.Error = fmt.Errorf("conflict resolution failed for %s: %w", c.Field, err)
				return result, result.Error
			}
		}
		result.ConflictsResolved = true

		if err := s.Commit(ctx, fmt.Sprintf("Resolve conflicts from %s using %s strategy", peer, strategy)); err != nil {
			result.Error = fmt.Errorf("commit conflict resolution: %w", err)
			return result, result.Error
		}

		// bd-578h9.11: the conflicted merge skipped the automatic is_blocked
		// recompute (unresolved rows would have fed it garbage); now that the
		// resolution is committed, cover the whole merge+resolution window.
		if err := s.RecomputeBlockedAfterMerge(ctx, beforeCommit); err != nil {
			result.Error = fmt.Errorf("conflicts resolved but is_blocked recompute failed: %w", err)
			return result, result.Error
		}
	}
	// On the bootstrap path (beads-aapwu) no merge happened — leave Merged
	// false and PulledCommits 0 so the result stays truthful; the value of a
	// first sync of an empty peer is entirely in the publishing push below.
	if !bootstrap {
		result.Merged = true

		afterCommit, _ := s.GetCurrentCommit(ctx)
		if beforeCommit != afterCommit {
			result.PulledCommits = 1
		}
	}

	// Step 5: Push our changes to peer, filtering excluded types.
	//
	// beads-t129: the exclude_types PRIVACY filter (beads-lgda) was implemented
	// ONLY on the server DoltStore.Sync path (dolt/federation.go
	// filteredPushToPeer). The embedded backend pushed UNFILTERED — a plain
	// PushTo — so a `bd federation sync` on a default (embedded) workspace
	// published every excluded/private issue type to the peer, and did so on
	// EVERY sync (the filter was simply absent), leaking by default (the
	// default exclude_types is ["wisp"]). Route through the same fail-CLOSED
	// filtered push so both backends honor the confidentiality boundary.
	excludeTypes := config.GetFederationConfig().ExcludeTypes
	if err := s.filteredPushToPeer(ctx, peer, excludeTypes); err != nil {
		// Push failure is not fatal - peer may not accept pushes.
		result.PushError = err
	} else {
		result.Pushed = true
	}

	// Record last sync time in metadata.
	_ = s.setLastSyncTime(ctx, peer)

	result.EndTime = time.Now()
	return result, nil
}

// filteredPushToPeer pushes to a peer after filtering out excluded issue types,
// mirroring the server DoltStore's filteredPushToPeer (dolt/federation.go) so
// the embedded backend honors the exclude_types privacy boundary the same way
// (beads-t129). When excludeTypes is empty, delegates directly to PushTo (no
// filtering).
//
// For non-empty excludeTypes, the method creates a temporary staging branch on
// a single pinned connection (branch operations are session-scoped), deletes
// matching issues, commits the filtered state, and pushes the staging branch to
// the peer mapped onto the peer's expected branch name. The staging branch is
// always cleaned up.
//
// A delete error is FATAL (fail CLOSED): exclude_types is a privacy filter, so
// if we cannot remove an excluded type we must NOT push the staging branch —
// doing so would leak the very issues the filter exists to withhold (the
// beads-lgda failure mode, applied here to the embedded backend).
//
// The special type "wisp" matches issues with ephemeral=true in the committed
// issues table (defense-in-depth: wisps normally live in dolt_ignore'd tables
// and are not pushed).
func (s *EmbeddedDoltStore) filteredPushToPeer(ctx context.Context, peer string, excludeTypes []string) error {
	if len(excludeTypes) == 0 {
		return s.PushTo(ctx, peer)
	}

	return s.withMutatingPinnedDBConn(ctx, func(db versioncontrolops.DBConn) error {
		// Clean up any leftover staging branch from a previous failed run.
		_, _ = db.ExecContext(ctx, "CALL DOLT_BRANCH('-Df', ?)", federationStagingBranch)

		// Create staging branch from the current branch.
		if _, err := db.ExecContext(ctx, "CALL DOLT_BRANCH(?, ?)", federationStagingBranch, s.branch); err != nil {
			return fmt.Errorf("federation filter: create staging branch: %w", err)
		}

		// Ensure cleanup: restore original branch and delete staging.
		defer func() {
			_, _ = db.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", s.branch)
			_, _ = db.ExecContext(ctx, "CALL DOLT_BRANCH('-Df', ?)", federationStagingBranch)
		}()

		// Checkout staging branch.
		if err := versioncontrolops.CheckoutBranch(ctx, db, federationStagingBranch); err != nil {
			return fmt.Errorf("federation filter: checkout staging: %w", err)
		}

		// Delete excluded issues from the committed issues table. A delete
		// error MUST be fatal (fail CLOSED) — see the method doc.
		deleted := false
		for _, excludeType := range excludeTypes {
			var result sql.Result
			var execErr error
			if excludeType == "wisp" {
				result, execErr = db.ExecContext(ctx, "DELETE FROM issues WHERE ephemeral = 1")
			} else {
				result, execErr = db.ExecContext(ctx, "DELETE FROM issues WHERE issue_type = ?", excludeType)
			}
			if execErr != nil {
				return fmt.Errorf("federation filter: delete excluded type %q (aborting push to avoid leaking it): %w", excludeType, execErr)
			}
			if n, _ := result.RowsAffected(); n > 0 {
				deleted = true
			}
		}

		if deleted {
			if _, err := db.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', ?)",
				"federation: exclude private issue types"); err != nil {
				return fmt.Errorf("federation filter: commit filtered state: %w", err)
			}
		}

		// Restore original branch context before pushing.
		if err := versioncontrolops.CheckoutBranch(ctx, db, s.branch); err != nil {
			return fmt.Errorf("federation filter: restore branch %s: %w", s.branch, err)
		}

		// Push staging branch to peer, mapped to the peer's expected branch
		// name. DOLT_PUSH accepts a "local:remote" refspec as the branch arg.
		refspec := federationStagingBranch + ":" + s.branch
		return versioncontrolops.Push(ctx, db, peer, refspec, remoteAuthUser())
	})
}

// SyncStatus returns the synchronization status with a peer.
func (s *EmbeddedDoltStore) SyncStatus(ctx context.Context, peer string) (*storage.SyncStatus, error) {
	status := &storage.SyncStatus{
		Peer: peer,
	}

	// Get ahead/behind counts by comparing refs.
	// Dolt's AS OF requires a literal ref, not a parameterized expression.
	remoteRef := peer + "/" + s.branch
	if err := issueops.ValidateRef(remoteRef); err != nil {
		status.LocalAhead = -1
		status.LocalBehind = -1
	} else if err := s.withDBConn(ctx, func(db versioncontrolops.DBConn) error {
		query := fmt.Sprintf(`
			SELECT
				(SELECT COUNT(*) FROM dolt_log WHERE commit_hash NOT IN
					(SELECT commit_hash FROM dolt_log AS OF '%s')) as ahead,
				(SELECT COUNT(*) FROM dolt_log AS OF '%s' WHERE commit_hash NOT IN
					(SELECT commit_hash FROM dolt_log)) as behind
		`, remoteRef, remoteRef)
		if err := db.QueryRowContext(ctx, query).
			Scan(&status.LocalAhead, &status.LocalBehind); err != nil {
			// Remote branch may not exist locally yet.
			status.LocalAhead = -1
			status.LocalBehind = -1
		}
		return nil
	}); err != nil {
		// beads-628e: return a PARTIAL (non-nil) status on a connection
		// failure, mirroring the server DoltStore.SyncStatus contract (which
		// always returns a status, never nil). Returning (nil, err) here made
		// callers that ignore the error — e.g. runFederationStatus's
		// `status, _ := ...` — nil-deref/panic in the render loop. The unknown
		// ahead/behind is signalled with -1, same as the query-failure branch.
		status.LocalAhead = -1
		status.LocalBehind = -1
		return status, fmt.Errorf("sync status for peer %s: %w", peer, err)
	}

	// Check for conflicts.
	conflicts, err := s.GetConflicts(ctx)
	if err == nil && len(conflicts) > 0 {
		status.HasConflicts = true
	}

	// Get last sync time.
	status.LastSync = s.getLastSyncTime(ctx, peer)

	return status, nil
}

// setLastSyncTime records the last sync time for a peer in metadata.
func (s *EmbeddedDoltStore) setLastSyncTime(ctx context.Context, peer string) error {
	key := "last_sync_" + peer
	value := time.Now().Format(time.RFC3339)
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"REPLACE INTO metadata (`key`, value) VALUES (?, ?)", key, value)
		return err
	})
}

// getLastSyncTime retrieves the last sync time for a peer from metadata.
func (s *EmbeddedDoltStore) getLastSyncTime(ctx context.Context, peer string) time.Time {
	key := "last_sync_" + peer
	var value string
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE `key` = ?", key).Scan(&value)
	})
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
