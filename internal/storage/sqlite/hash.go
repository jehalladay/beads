package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

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
