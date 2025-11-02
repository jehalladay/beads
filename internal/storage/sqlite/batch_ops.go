package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// validateBatchIssues validates all issues in a batch and sets timestamps if not provided
func validateBatchIssues(issues []*types.Issue) error {
	now := time.Now()
	for i, issue := range issues {
		if issue == nil {
			return fmt.Errorf("issue %d is nil", i)
		}

		// Only set timestamps if not already provided
		if issue.CreatedAt.IsZero() {
			issue.CreatedAt = now
		}
		if issue.UpdatedAt.IsZero() {
			issue.UpdatedAt = now
		}

		if err := issue.Validate(); err != nil {
			return fmt.Errorf("validation failed for issue %d: %w", i, err)
		}
	}
	return nil
}

// generateBatchIDs generates IDs for all issues that need them atomically
func generateBatchIDs(ctx context.Context, conn *sql.Conn, issues []*types.Issue, actor string) error {
	// Get prefix from config (needed for both generation and validation)
	var prefix string
	err := conn.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, "issue_prefix").Scan(&prefix)
	if err == sql.ErrNoRows || prefix == "" {
		// CRITICAL: Reject operation if issue_prefix config is missing (bd-166)
		return fmt.Errorf("database not initialized: issue_prefix config is missing (run 'bd init --prefix <prefix>' first)")
	} else if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Validate explicitly provided IDs and generate IDs for those that need them
	expectedPrefix := prefix + "-"
	usedIDs := make(map[string]bool)
	
	// First pass: record explicitly provided IDs
	for i := range issues {
		if issues[i].ID != "" {
			// Validate that explicitly provided ID matches the configured prefix (bd-177)
			if !strings.HasPrefix(issues[i].ID, expectedPrefix) {
				return fmt.Errorf("issue ID '%s' does not match configured prefix '%s'", issues[i].ID, prefix)
			}
			usedIDs[issues[i].ID] = true
		}
	}
	
	// Second pass: generate IDs for issues that need them
	// Hash mode: generate with adaptive length based on database size (bd-ea2a13)
	// Get adaptive base length based on current database size
	baseLength, err := GetAdaptiveIDLength(ctx, conn, prefix)
	if err != nil {
		// Fallback to 6 on error
		baseLength = 6
	}
	
	// Try baseLength, baseLength+1, baseLength+2, up to max of 8
	maxLength := 8
	if baseLength > maxLength {
		baseLength = maxLength
	}
	
	for i := range issues {
		if issues[i].ID == "" {
			var generated bool
			// Try lengths from baseLength to maxLength with progressive fallback
			for length := baseLength; length <= maxLength && !generated; length++ {
				for nonce := 0; nonce < 10; nonce++ {
					candidate := generateHashID(prefix, issues[i].Title, issues[i].Description, actor, issues[i].CreatedAt, length, nonce)
					
					// Check if this ID is already used in this batch or in the database
					if usedIDs[candidate] {
						continue
					}
					
					var count int
					err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE id = ?`, candidate).Scan(&count)
					if err != nil {
						return fmt.Errorf("failed to check for ID collision: %w", err)
					}
					
					if count == 0 {
						issues[i].ID = candidate
						usedIDs[candidate] = true
						generated = true
						break
					}
				}
			}
			
			if !generated {
				return fmt.Errorf("failed to generate unique ID for issue %d after trying lengths 6-8 with 10 nonces each", i)
			}
		}
	}
	
	// Compute content hashes
	for i := range issues {
		if issues[i].ContentHash == "" {
			issues[i].ContentHash = issues[i].ComputeContentHash()
		}
	}
	return nil
}

// bulkInsertIssues inserts all issues using a prepared statement
func bulkInsertIssues(ctx context.Context, conn *sql.Conn, issues []*types.Issue) error {
	stmt, err := conn.PrepareContext(ctx, `
		INSERT INTO issues (
			id, content_hash, title, description, design, acceptance_criteria, notes,
			status, priority, issue_type, assignee, estimated_minutes,
			created_at, updated_at, closed_at, external_ref
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, issue := range issues {
		_, err = stmt.ExecContext(ctx,
			issue.ID, issue.ContentHash, issue.Title, issue.Description, issue.Design,
			issue.AcceptanceCriteria, issue.Notes, issue.Status,
			issue.Priority, issue.IssueType, issue.Assignee,
			issue.EstimatedMinutes, issue.CreatedAt, issue.UpdatedAt,
			issue.ClosedAt, issue.ExternalRef,
		)
		if err != nil {
			return fmt.Errorf("failed to insert issue %s: %w", issue.ID, err)
		}
	}
	return nil
}

// bulkRecordEvents records creation events for all issues
func bulkRecordEvents(ctx context.Context, conn *sql.Conn, issues []*types.Issue, actor string) error {
	stmt, err := conn.PrepareContext(ctx, `
		INSERT INTO events (issue_id, event_type, actor, new_value)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare event statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, issue := range issues {
		eventData, err := json.Marshal(issue)
		if err != nil {
			// Fall back to minimal description if marshaling fails
			eventData = []byte(fmt.Sprintf(`{"id":"%s","title":"%s"}`, issue.ID, issue.Title))
		}

		_, err = stmt.ExecContext(ctx, issue.ID, types.EventCreated, actor, string(eventData))
		if err != nil {
			return fmt.Errorf("failed to record event for %s: %w", issue.ID, err)
		}
	}
	return nil
}

// bulkMarkDirty marks all issues as dirty for incremental export
func bulkMarkDirty(ctx context.Context, conn *sql.Conn, issues []*types.Issue) error {
	stmt, err := conn.PrepareContext(ctx, `
		INSERT INTO dirty_issues (issue_id, marked_at)
		VALUES (?, ?)
		ON CONFLICT (issue_id) DO UPDATE SET marked_at = excluded.marked_at
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare dirty statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	dirtyTime := time.Now()
	for _, issue := range issues {
		_, err = stmt.ExecContext(ctx, issue.ID, dirtyTime)
		if err != nil {
			return fmt.Errorf("failed to mark dirty %s: %w", issue.ID, err)
		}
	}
	return nil
}

// CreateIssues creates multiple issues atomically in a single transaction.
// This provides significant performance improvements over calling CreateIssue in a loop:
// - Single connection acquisition
// - Single transaction
// - Atomic ID range reservation (one counter update for N issues)
// - All-or-nothing atomicity
//
// Expected 5-10x speedup for batches of 10+ issues.
// CreateIssues creates multiple issues atomically in a single transaction.
//
// This method is optimized for bulk issue creation and provides significant
// performance improvements over calling CreateIssue in a loop:
//   - Single database connection and transaction
//   - Atomic ID range reservation (one counter update for N IDs)
//   - All-or-nothing semantics (rolls back on any error)
//   - 5-15x faster than sequential CreateIssue calls
//
// All issues are validated before any database changes occur. If any issue
// fails validation, the entire batch is rejected.
//
// ID Assignment:
//   - Issues with empty ID get auto-generated IDs from a reserved range
//   - Issues with explicit IDs use those IDs (caller must ensure uniqueness)
//   - Mix of explicit and auto-generated IDs is supported
//
// Timestamps:
//   - All issues in the batch receive identical created_at/updated_at timestamps
//   - This reflects that they were created as a single atomic operation
//
// Usage:
//   // Bulk import from external source
//   issues := []*types.Issue{...}
//   if err := store.CreateIssues(ctx, issues, "import"); err != nil {
//       return err
//   }
//
//   // After importing with explicit IDs, sync counters to prevent collisions
// REMOVED (bd-c7af): SyncAllCounters example - no longer needed with hash IDs
//
// Performance:
//   - 100 issues: ~30ms (vs ~900ms with CreateIssue loop)
//   - 1000 issues: ~950ms (vs estimated 9s with CreateIssue loop)
//
// When to use:
//   - Bulk imports from external systems (use CreateIssues)
//   - Creating multiple related issues at once (use CreateIssues)
//   - Single issue creation (use CreateIssue for simplicity)
//   - Interactive user operations (use CreateIssue)
func (s *SQLiteStorage) CreateIssues(ctx context.Context, issues []*types.Issue, actor string) error {
	if len(issues) == 0 {
		return nil
	}

	// Phase 1: Validate all issues first (fail-fast)
	if err := validateBatchIssues(issues); err != nil {
		return err
	}

	// Phase 2: Acquire connection and start transaction
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("failed to begin immediate transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	// Phase 3: Generate IDs for issues that need them
	if err := generateBatchIDs(ctx, conn, issues, actor); err != nil {
		return err
	}

	// Phase 4: Bulk insert issues
	if err := bulkInsertIssues(ctx, conn, issues); err != nil {
		return err
	}

	// Phase 5: Record creation events
	if err := bulkRecordEvents(ctx, conn, issues, actor); err != nil {
		return err
	}

	// Phase 6: Mark issues dirty for incremental export
	if err := bulkMarkDirty(ctx, conn, issues); err != nil {
		return err
	}

	// Phase 7: Commit transaction
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true
	return nil
}
