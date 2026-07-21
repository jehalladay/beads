package issueops

import (
	"context"
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// GetStaleIssuesInTx returns issues that haven't been updated within the
// given number of days. Only non-ephemeral issues are considered. When
// filter.Status is empty, open and in_progress issues are returned.
// Results are ordered by updated_at ascending (stalest first).
//
// nolint:gosec // G201: statusClause contains only literal SQL or a single ? placeholder
func GetStaleIssuesInTx(ctx context.Context, tx DBTX, filter types.StaleFilter) ([]*types.Issue, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -filter.Days)

	statusClause := "status IN ('open', 'in_progress')"
	args := []interface{}{cutoff}
	switch {
	case filter.Status == "":
		// default: open + in_progress
	case filter.Status == string(types.StatusBlocked):
		// beads-h40fl: "blocked" is a DERIVED pseudo-status, not a stored
		// status value — a blocked issue keeps stored status='open'/'in_progress'
		// and its blocked-ness lives in the denormalized is_blocked column. A
		// plain `status = 'blocked'` clause is unsatisfiable by construction, so
		// `bd stale --status blocked` silently returned 0 with rc=0 (false
		// negative) even when stale blocked issues existed. Route to the
		// is_blocked predicate, matching the beads-7f3g routing for
		// `bd list/count --status blocked` and bd blocked's semantics
		// (issueops/blocked.go:247), incl. its closed/pinned exclusion.
		statusClause = "is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'"
	default:
		statusClause = "status = ?"
		args = append(args, filter.Status)
	}

	query := fmt.Sprintf(`
		SELECT id FROM issues
		WHERE updated_at < ?
		  AND %s
		  AND (ephemeral = 0 OR ephemeral IS NULL)
		ORDER BY updated_at ASC
	`, statusClause)

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get stale issues: %w", err)
	}

	// Collect IDs first, then batch-fetch full issues.
	// Close rows explicitly before the nested fetch — MySQL/Dolt drivers
	// can't handle multiple active result sets on one connection.
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan stale issue id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("stale issues rows: %w", err)
	}
	rows.Close()

	if len(ids) == 0 {
		return nil, nil
	}

	// GetIssuesByIDsInTx returns issues in arbitrary order (WHERE IN),
	// so re-order to preserve the updated_at ASC ordering from the query.
	issues, err := GetIssuesByIDsInTx(ctx, tx, ids, nil)
	if err != nil {
		return nil, err
	}

	issueByID := make(map[string]*types.Issue, len(issues))
	for _, iss := range issues {
		if iss != nil {
			issueByID[iss.ID] = iss
		}
	}

	ordered := make([]*types.Issue, 0, len(ids))
	for _, id := range ids {
		if iss, ok := issueByID[id]; ok {
			ordered = append(ordered, iss)
		}
	}

	return ordered, nil
}
