package issueops

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// validRefPattern matches valid Dolt commit hashes (32 hex chars) or branch names.
// Allows dots and slashes for branch names like "release/v2.0" or "feature/auth.flow".
var validRefPattern = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)

// ValidateRef checks if a ref string is safe to use in AS OF queries.
// Refs must be non-empty, <= 128 chars, and match [a-zA-Z0-9_./-]+.
func ValidateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("ref cannot be empty")
	}
	if len(ref) > 128 {
		return fmt.Errorf("ref too long")
	}
	if !validRefPattern.MatchString(ref) {
		return fmt.Errorf("invalid ref format: %s", ref)
	}
	return nil
}

// AsOfInTx returns the state of an issue at a specific commit hash or branch ref.
// Uses Dolt's AS OF syntax which works in both server and embedded modes.
//
// nolint:gosec // G201: ref is validated by ValidateRef() above - AS OF requires literal
func AsOfInTx(ctx context.Context, tx DBTX, issueID string, ref string) (*types.Issue, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, fmt.Errorf("invalid ref: %w", err)
	}

	var issue types.Issue
	var createdAtStr, updatedAtStr sql.NullString
	var closedAt, startedAt sql.NullTime
	var assignee, owner, contentHash sql.NullString
	var design, acceptanceCriteria, notes sql.NullString
	var closeReason, specID, molType sql.NullString
	var estimatedMinutes sql.NullInt64
	var pinned sql.NullInt64

	// Project the full field set (matching HistoryInTx), not a 14-column subset.
	// A subset silently omitted design/notes/acceptance_criteria/spec_id from the
	// historical view, so `bd show --as-of --json` returned a structurally
	// incomplete issue vs live `bd show --json` and vs History (beads-kpfp).
	query := fmt.Sprintf(`
		SELECT id, content_hash, title, description, design, acceptance_criteria, notes,
		       status, priority, issue_type, assignee, estimated_minutes,
		       created_at, created_by, owner, updated_at, started_at, closed_at,
		       close_reason, spec_id, pinned, mol_type
		FROM issues AS OF '%s'
		WHERE id = ?
	`, ref)

	err := tx.QueryRowContext(ctx, query, issueID).Scan(
		&issue.ID, &contentHash, &issue.Title, &issue.Description, &design, &acceptanceCriteria, &notes,
		&issue.Status, &issue.Priority, &issue.IssueType, &assignee, &estimatedMinutes,
		&createdAtStr, &issue.CreatedBy, &owner, &updatedAtStr, &startedAt, &closedAt,
		&closeReason, &specID, &pinned, &molType,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: issue %s as of %s", storage.ErrNotFound, issueID, ref)
	}
	if err != nil {
		return nil, fmt.Errorf("get issue as of %s: %w", ref, err)
	}

	if createdAtStr.Valid {
		issue.CreatedAt = ParseTimeString(createdAtStr.String)
	}
	if updatedAtStr.Valid {
		issue.UpdatedAt = ParseTimeString(updatedAtStr.String)
	}
	if contentHash.Valid {
		issue.ContentHash = contentHash.String
	}
	if design.Valid {
		issue.Design = design.String
	}
	if acceptanceCriteria.Valid {
		issue.AcceptanceCriteria = acceptanceCriteria.String
	}
	if notes.Valid {
		issue.Notes = notes.String
	}
	if startedAt.Valid {
		issue.StartedAt = &startedAt.Time
	}
	if closedAt.Valid {
		issue.ClosedAt = &closedAt.Time
	}
	if assignee.Valid {
		issue.Assignee = assignee.String
	}
	if owner.Valid {
		issue.Owner = owner.String
	}
	if estimatedMinutes.Valid {
		mins := int(estimatedMinutes.Int64)
		issue.EstimatedMinutes = &mins
	}
	if closeReason.Valid {
		issue.CloseReason = closeReason.String
	}
	if specID.Valid {
		issue.SpecID = specID.String
	}
	if pinned.Valid && pinned.Int64 != 0 {
		issue.Pinned = true
	}
	if molType.Valid {
		issue.MolType = types.MolType(molType.String)
	}

	return &issue, nil
}
