package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// getNextChildNumber atomically increments and returns the next child counter for a parent issue.
// Uses INSERT...ON CONFLICT to ensure atomicity without explicit locking.
func (s *SQLiteStorage) getNextChildNumber(ctx context.Context, parentID string) (int, error) {
	var nextChild int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO child_counters (parent_id, last_child)
		VALUES (?, 1)
		ON CONFLICT(parent_id) DO UPDATE SET
			last_child = last_child + 1
		RETURNING last_child
	`, parentID).Scan(&nextChild)
	if err != nil {
		return 0, fmt.Errorf("failed to generate next child number for parent %s: %w", parentID, err)
	}
	return nextChild, nil
}

// GetNextChildID generates the next hierarchical child ID for a given parent
// Returns formatted ID as parentID.{counter} (e.g., bd-a3f8e9.1 or bd-a3f8e9.1.5)
// Works at any depth (max 3 levels)
func (s *SQLiteStorage) GetNextChildID(ctx context.Context, parentID string) (string, error) {
	// Validate parent exists
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE id = ?`, parentID).Scan(&count)
	if err != nil {
		return "", fmt.Errorf("failed to check parent existence: %w", err)
	}
	if count == 0 {
		return "", fmt.Errorf("parent issue %s does not exist", parentID)
	}
	
	// Calculate current depth by counting dots
	depth := strings.Count(parentID, ".")
	if depth >= 3 {
		return "", fmt.Errorf("maximum hierarchy depth (3) exceeded for parent %s", parentID)
	}
	
	// Get next child number atomically
	nextNum, err := s.getNextChildNumber(ctx, parentID)
	if err != nil {
		return "", err
	}
	
	// Format as parentID.counter
	childID := fmt.Sprintf("%s.%d", parentID, nextNum)
	return childID, nil
}

// generateHashID creates a hash-based ID for a top-level issue.
// For child issues, use the parent ID with a numeric suffix (e.g., "bd-a3f8e9.1").
// Supports adaptive length from 4-8 chars based on database size (bd-ea2a13).
// Includes a nonce parameter to handle same-length collisions.
func generateHashID(prefix, title, description, creator string, timestamp time.Time, length, nonce int) string {
	// Combine inputs into a stable content string
	// Include nonce to handle hash collisions
	content := fmt.Sprintf("%s|%s|%s|%d|%d", title, description, creator, timestamp.UnixNano(), nonce)
	
	// Hash the content
	hash := sha256.Sum256([]byte(content))
	
	// Use variable length (4-8 hex chars)
	// length determines how many bytes to use (2, 2.5, 3, 3.5, or 4)
	var shortHash string
	switch length {
	case 4:
		shortHash = hex.EncodeToString(hash[:2])
	case 5:
		// 2.5 bytes: use 3 bytes but take only first 5 chars
		shortHash = hex.EncodeToString(hash[:3])[:5]
	case 6:
		shortHash = hex.EncodeToString(hash[:3])
	case 7:
		// 3.5 bytes: use 4 bytes but take only first 7 chars
		shortHash = hex.EncodeToString(hash[:4])[:7]
	case 8:
		shortHash = hex.EncodeToString(hash[:4])
	default:
		shortHash = hex.EncodeToString(hash[:3]) // default to 6
	}
	
	return fmt.Sprintf("%s-%s", prefix, shortHash)
}
