package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// computeIssueContentHash computes a SHA256 hash of an issue's content, excluding timestamps.
// This is used for detecting timestamp-only changes during export deduplication.
func computeIssueContentHash(issue *types.Issue) (string, error) {
	// Clone issue and zero out timestamps to exclude them from hash
	normalized := *issue
	normalized.CreatedAt = time.Time{}
	normalized.UpdatedAt = time.Time{}
	
	// Also zero out ClosedAt if present
	if normalized.ClosedAt != nil {
		zeroTime := time.Time{}
		normalized.ClosedAt = &zeroTime
	}
	
	// Serialize to JSON
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	
	// SHA256 hash
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// GetExportHash retrieves the content hash of the last export for an issue.
// Returns empty string if no hash is stored (first export).
func (s *SQLiteStorage) GetExportHash(ctx context.Context, issueID string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `
		SELECT content_hash FROM export_hashes WHERE issue_id = ?
	`, issueID).Scan(&hash)
	
	if err == sql.ErrNoRows {
		return "", nil // No hash stored yet
	}
	if err != nil {
		return "", fmt.Errorf("failed to get export hash for %s: %w", issueID, err)
	}
	
	return hash, nil
}

// SetExportHash stores the content hash of an issue after successful export.
func (s *SQLiteStorage) SetExportHash(ctx context.Context, issueID, contentHash string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO export_hashes (issue_id, content_hash, exported_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(issue_id) DO UPDATE SET
			content_hash = excluded.content_hash,
			exported_at = CURRENT_TIMESTAMP
	`, issueID, contentHash)
	
	if err != nil {
		return fmt.Errorf("failed to set export hash for %s: %w", issueID, err)
	}
	
	return nil
}

// ClearAllExportHashes removes all export hashes from the database.
// This is primarily used for test isolation to force re-export of issues.
func (s *SQLiteStorage) ClearAllExportHashes(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM export_hashes`)
	if err != nil {
		return fmt.Errorf("failed to clear export hashes: %w", err)
	}
	return nil
}
