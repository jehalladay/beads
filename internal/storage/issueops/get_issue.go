package issueops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// GetIssueInTx retrieves a single issue by ID within an existing transaction,
// including its labels. Automatically routes to the wisps/wisp_labels tables
// if the ID is an active wisp. Returns storage.ErrNotFound (wrapped) if the
// issue does not exist in either table.
func GetIssueInTx(ctx context.Context, tx DBTX, id string) (*types.Issue, error) {
	// Single-connection callers (embeddeddolt, the DoltStore read path) see both
	// the issues and wisps tables on one tx, so both table reads share it.
	return GetIssueInTxSplit(ctx, tx, tx, id)
}

// GetIssueInTxSplit is the two-connection form of GetIssueInTx. It reads the
// issues table on issuesTx and, on a miss, the wisps table on wispsTx.
//
// The Dolt SQL-server transaction (dolt.doltTransaction) pins issues to a
// versioned connection and wisps to a separate dolt_ignore'd connection so that
// same-transaction wisp writes are read-your-own-writes visible (and DOLT_COMMIT
// does not stage the ignored tables). Passing that split here lets the shared
// issueops SQL retire the hand-copied in-tx GetIssue (beads-898t2) without
// collapsing the two connections. Callers with a single connection use
// GetIssueInTx, which passes the same tx for both.
func GetIssueInTxSplit(ctx context.Context, issuesTx, wispsTx DBTX, id string) (*types.Issue, error) {
	issue, err := getIssueFromTableInTx(ctx, issuesTx, "issues", "labels", id)
	if err == nil {
		return issue, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}

	issue, err = getIssueFromTableInTx(ctx, wispsTx, "wisps", "wisp_labels", id)
	if err == nil {
		return issue, nil
	}
	if errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("%w: issue %s", storage.ErrNotFound, id)
	}
	return nil, err
}

func getIssueFromTableInTx(ctx context.Context, tx DBTX, issueTable, labelTable, id string) (*types.Issue, error) {
	//nolint:gosec // G201: issueTable is a hardcoded literal supplied by GetIssueInTx ("issues" or "wisps")
	row := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE id = ?`, IssueSelectColumns, issueTable), id)
	issue, err := ScanIssueFrom(row)
	if err == sql.ErrNoRows || isTableNotExistError(err) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get issue: %w", err)
	}

	// Fetch labels in the same transaction to avoid MaxOpenConns=1 deadlock.
	labels, err := GetLabelsInTx(ctx, tx, labelTable, id)
	if err != nil {
		return nil, fmt.Errorf("get issue labels: %w", err)
	}
	issue.Labels = labels

	return issue, nil
}
