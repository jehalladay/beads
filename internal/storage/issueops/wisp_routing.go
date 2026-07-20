package issueops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// IsActiveWispInTx checks whether the given ID exists in the wisps table
// within an existing transaction. Returns true if the ID is found.
// This handles both auto-generated wisp IDs (containing "-wisp-") and
// ephemeral issues created with explicit IDs that were routed to wisps.
//
// For hot-path callers that partition a batch of IDs by wisp status,
// prefer WispIDSetInTx + partitionByWispSet to amortize the per-ID
// query cost into a single scoped query over the batch.
//
// beads-byr7: a bare `err == nil` on the wisps probe used to swallow ALL
// errors as "not a wisp", so a TRANSIENT (non-ErrNoRows) probe error on a
// row that IS a wisp misrouted every write-routing caller to the `issues`
// table (UPDATE issues WHERE id=<wisp> → 0 rows = silent lost update, event
// to the wrong table). GetIssueInTx masked it because it checks BOTH tables.
// The bool signature is preserved (32 write-routing callers depend on it);
// the fix is contained here: on a transient wisp-probe error, disambiguate
// against the `issues` table (the same both-table logic GetIssueInTx uses)
// so the returned bool is correct — found in issues → false (it's an issue),
// clean-negative in issues → true (fail toward wisp; the wisp probe is the
// one that errored, and a spurious wisp route is a harmless 0-row update if
// the row is genuinely gone). Only a DOUBLE probe failure falls back to the
// old false (no worse than the prior behavior).
func IsActiveWispInTx(ctx context.Context, tx DBTX, id string) bool {
	isWisp, err := isActiveWispInTxE(ctx, tx, id)
	if err == nil {
		return isWisp
	}
	// Wisp probe hit a transient error — disambiguate via the issues table
	// rather than silently assuming "not a wisp".
	inIssues, issuesErr := idExistsInTableInTx(ctx, tx, "issues", id)
	if issuesErr != nil {
		// Both probes failed: cannot determine table. Preserve the historical
		// bool (false) — no worse than the pre-byr7 behavior.
		return false
	}
	// If the row is a permanent issue, it is not a wisp. Otherwise the wisp
	// probe was the failing one, so route as a wisp.
	return !inIssues
}

// isActiveWispInTxE is IsActiveWispInTx with an explicit error, modelling the
// three cases the sibling probes already distinguish: a matching row → true;
// no row / missing wisps table → false; any other (transient) error → the
// error propagated so the caller can disambiguate instead of guessing false.
func isActiveWispInTxE(ctx context.Context, tx DBTX, id string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM wisps WHERE id = ? LIMIT 1", id).Scan(&exists)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case isTableNotExistError(err):
		return false, nil
	default:
		return false, err
	}
}

// idExistsInTableInTx reports whether id has a row in the named table, treating
// a missing table as "absent" (mirrors the ErrNoRows/table-not-exist handling
// of the sibling probes). Any other error propagates.
//
//nolint:gosec // G201: table is a hardcoded literal supplied by callers in this file.
func idExistsInTableInTx(ctx context.Context, tx DBTX, table, id string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT 1 FROM %s WHERE id = ? LIMIT 1", table), id).Scan(&exists)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case isTableNotExistError(err):
		return false, nil
	default:
		return false, err
	}
}

func wispsTableEmptyOrMissingInTx(ctx context.Context, tx DBTX) (bool, error) {
	var probe int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM wisps LIMIT 1").Scan(&probe)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, sql.ErrNoRows):
		return true, nil
	case isTableNotExistError(err):
		return true, nil
	default:
		return false, err
	}
}

//nolint:gosec // table is selected by callers from fixed optional wisp tables.
func optionalTableExistsInTx(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var probe int
	err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", table)).Scan(&probe)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return true, nil
	case isTableNotExistError(err):
		return false, nil
	default:
		return false, err
	}
}

// WispIDSetInTx returns the subset of ids that are currently-active wisps
// within the tx. The set is consistent for the tx's lifetime (Dolt MVCC).
// Intended for hot-path partitioning where a batch of IDs must be split
// into wisps vs permanents; one scoped query amortized over the batch
// replaces N per-ID IsActiveWispInTx calls without paying for a full
// wisps-table scan when callers have a small batch against a large
// wisps table.
//
// Returns an empty set when ids is empty; never issues a query.
//
//nolint:gosec // G201: query uses placeholder-only interpolation
func WispIDSetInTx(ctx context.Context, tx DBTX, ids []string) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	if len(ids) == 0 {
		return set, nil
	}
	if empty, err := wispsTableEmptyOrMissingInTx(ctx, tx); err != nil {
		return nil, fmt.Errorf("wisp id set: probe: %w", err)
	} else if empty {
		return set, nil
	}
	for start := 0; start < len(ids); start += queryBatchSize {
		end := start + queryBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = id
		}
		q := fmt.Sprintf("SELECT id FROM wisps WHERE id IN (%s)", strings.Join(placeholders, ","))
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("wisp id set: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("wisp id set: scan: %w", err)
			}
			set[id] = struct{}{}
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("wisp id set: rows: %w", err)
		}
	}
	return set, nil
}

// partitionByWispSet splits ids into (wispIDs, permIDs) using the provided
// wisp-id set. If wispSet is nil the caller must populate it first via
// WispIDSetInTx; this helper does no I/O.
func partitionByWispSet(ids []string, wispSet map[string]struct{}) (wispIDs, permIDs []string) {
	for _, id := range ids {
		if _, isWisp := wispSet[id]; isWisp {
			wispIDs = append(wispIDs, id)
		} else {
			permIDs = append(permIDs, id)
		}
	}
	return wispIDs, permIDs
}

// PartitionWispIDsInTx partitions a set of IDs into wisp vs non-wisp buckets
// using a single batched `SELECT id FROM wisps WHERE id IN (...)` query per
// queryBatchSize chunk, rather than one round-trip per ID. This is critical
// for remote backends (Dolt) where per-ID round-trips multiply WAN latency
// and can push bulk hydration past the context deadline (see GH#3414).
// IDs not present in the wisps table are treated as permanent issue IDs.
// Returned slices preserve the input ordering within each bucket.
func PartitionWispIDsInTx(ctx context.Context, tx DBTX, ids []string) (wispIDs, permIDs []string, err error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}

	if empty, probeErr := wispsTableEmptyOrMissingInTx(ctx, tx); probeErr != nil {
		return nil, nil, fmt.Errorf("partition wisp ids: probe: %w", probeErr)
	} else if empty {
		return nil, append([]string(nil), ids...), nil
	}

	wispSet := make(map[string]struct{}, len(ids))
	for start := 0; start < len(ids); start += queryBatchSize {
		end := start + queryBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = id
		}
		//nolint:gosec // G201: only ? placeholders in the IN clause.
		rows, qErr := tx.QueryContext(ctx,
			fmt.Sprintf("SELECT id FROM wisps WHERE id IN (%s)", strings.Join(placeholders, ",")),
			args...)
		if qErr != nil {
			// Wisps table may not exist yet on older schemas — treat as "no wisps".
			if isTableNotExistError(qErr) {
				return nil, append([]string(nil), ids...), nil
			}
			return nil, nil, fmt.Errorf("partition wisp ids: %w", qErr)
		}
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				_ = rows.Close()
				return nil, nil, fmt.Errorf("partition wisp ids: scan: %w", scanErr)
			}
			wispSet[id] = struct{}{}
		}
		_ = rows.Close()
		if rowsErr := rows.Err(); rowsErr != nil {
			return nil, nil, fmt.Errorf("partition wisp ids: rows: %w", rowsErr)
		}
	}

	for _, id := range ids {
		if _, ok := wispSet[id]; ok {
			wispIDs = append(wispIDs, id)
		} else {
			permIDs = append(permIDs, id)
		}
	}
	return wispIDs, permIDs, nil
}

// WispTableRouting returns the appropriate issue, label, event, and dependency
// table names based on whether the ID is an active wisp. Call IsActiveWispInTx
// first to determine isWisp.
func WispTableRouting(isWisp bool) (issueTable, labelTable, eventTable, depTable string) {
	if isWisp {
		return "wisps", "wisp_labels", "wisp_events", "wisp_dependencies"
	}
	return "issues", "labels", "events", "dependencies"
}
