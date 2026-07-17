package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/steveyegge/beads/internal/storage/dberrors"
	"github.com/steveyegge/beads/internal/storage/domain"
)

func NewChildCounterSQLRepository(runner Runner) domain.ChildCounterSQLRepository {
	return &childCounterSQLRepositoryImpl{runner: runner}
}

type childCounterSQLRepositoryImpl struct {
	runner Runner
}

var _ domain.ChildCounterSQLRepository = (*childCounterSQLRepositoryImpl)(nil)

func (r *childCounterSQLRepositoryImpl) NextChildID(ctx context.Context, parentID string, _ domain.ChildCounterOpts) (string, error) {
	if parentID == "" {
		return "", errors.New("db: ChildCounterSQLRepository.NextChildID: parentID must not be empty")
	}

	counterTable, issueTable := "child_counters", "issues"
	parentIsWisp, err := r.parentIsActiveWisp(ctx, parentID)
	if err != nil {
		return "", fmt.Errorf("db: ChildCounterSQLRepository.NextChildID: probe parent table for %s: %w", parentID, err)
	}
	if parentIsWisp {
		counterTable, issueTable = "wisp_child_counters", "wisps"
	}

	var lastChild int
	err = r.runner.QueryRowContext(ctx,
		//nolint:gosec // G201: counterTable is one of two hardcoded constants
		fmt.Sprintf("SELECT last_child FROM %s WHERE parent_id = ?", counterTable),
		parentID,
	).Scan(&lastChild)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		lastChild = 0
	default:
		return "", fmt.Errorf("db: ChildCounterSQLRepository.NextChildID: read counter for %s: %w", parentID, err)
	}

	// Scan BOTH the parent's own table and the sibling table for existing
	// children. issues and wisps have no cross-table uniqueness, so scanning
	// only issueTable let the mint return parent.N while parent.N already lived
	// in the OTHER table (an orphaned wisp child left by promote, or an
	// imported/carried id), minting a same-id issue+wisp collision (beads-tnv9,
	// xaxe family). Bumping past a cross-table child keeps the mint fail-safe;
	// InsertIssueIfNew remains the hard fail-closed guard. Mirrors the direct
	// stack (issueops.GetNextChildIDTx) to avoid a direct-vs-proxied asymmetry.
	siblingTable := "wisps"
	if issueTable == "wisps" {
		siblingTable = "issues"
	}
	for _, table := range []string{issueTable, siblingTable} {
		rows, err := r.runner.QueryContext(ctx, fmt.Sprintf(`
			SELECT id FROM %s
			WHERE id LIKE CONCAT(?, '.%%')
			  AND id NOT LIKE CONCAT(?, '.%%.%%')
		`, table), parentID, parentID) //nolint:gosec // G201: table is one of the hardcoded constants above
		if err != nil {
			return "", fmt.Errorf("db: ChildCounterSQLRepository.NextChildID: scan existing children of %s: %w", parentID, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return "", fmt.Errorf("db: ChildCounterSQLRepository.NextChildID: scan: %w", err)
			}
			if n, ok := parseChildSuffix(id); ok && n > lastChild {
				lastChild = n
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", fmt.Errorf("db: ChildCounterSQLRepository.NextChildID: rows: %w", err)
		}
		rows.Close()
	}

	next := lastChild + 1
	//nolint:gosec // G201: counterTable is one of two hardcoded constants
	if _, err := r.runner.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (parent_id, last_child) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE last_child = ?
	`, counterTable), parentID, next, next); err != nil {
		return "", fmt.Errorf("db: ChildCounterSQLRepository.NextChildID: upsert counter for %s: %w", parentID, err)
	}

	return fmt.Sprintf("%s.%d", parentID, next), nil
}

func (r *childCounterSQLRepositoryImpl) parentIsActiveWisp(ctx context.Context, parentID string) (bool, error) {
	var probe int
	err := r.runner.QueryRowContext(ctx, "SELECT 1 FROM wisps WHERE id = ? LIMIT 1", parentID).Scan(&probe)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case dberrors.IsTableNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

func parseChildSuffix(id string) (int, bool) {
	dot := strings.LastIndex(id, ".")
	if dot < 0 || dot == len(id)-1 {
		return 0, false
	}
	n, err := strconv.Atoi(id[dot+1:])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
