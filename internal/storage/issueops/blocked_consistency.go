package issueops

import (
	"context"
	"fmt"
)

// RecomputeAllIsBlockedInTx recomputes the denormalized is_blocked column for
// every issue and wisp in one batched mark/unmark fixpoint and returns the
// number of rows it corrected.
//
// Unlike RecomputeIsBlockedAfterMergeInTx, which is scoped to a pull's diff and
// is skipped when a re-pull merges nothing (head == fromCommit), this is the
// always-available full repair: it does not depend on a merge advancing HEAD,
// so it can recover an is_blocked column left stale by a post-merge recompute
// that failed after its merge committed, or by a conflicted pull the operator
// resolved by hand (bd-6dnrw.37). It is idempotent — on a consistent database
// it changes nothing and returns 0.
func RecomputeAllIsBlockedInTx(ctx context.Context, tx DBTX) (int64, error) {
	issueIDs, err := allIDs(ctx, tx, "issues")
	if err != nil {
		return 0, fmt.Errorf("recompute all is_blocked: list issues: %w", err)
	}
	wispIDs, err := allIDs(ctx, tx, "wisps")
	if err != nil {
		if isTableNotExistError(err) {
			wispIDs = nil
		} else {
			return 0, fmt.Errorf("recompute all is_blocked: list wisps: %w", err)
		}
	}
	return recomputeIsBlockedCounting(ctx, tx, issueIDs, wispIDs)
}

// recomputeIsBlockedCounting is RecomputeIsBlockedInTx with a corrected-row
// count: it sums the rows flipped across every fixpoint pass. A parent flip in
// one pass can cascade to its children in the next, and each correction counts.
func recomputeIsBlockedCounting(ctx context.Context, tx DBTX, issueIDs, wispIDs []string) (int64, error) {
	if len(issueIDs) == 0 && len(wispIDs) == 0 {
		return 0, nil
	}
	var total int64
	for {
		var changed int64
		n, err := recomputeIsBlockedPassForIssuesInTx(ctx, tx, issueIDs)
		if err != nil {
			return total, err
		}
		changed += n
		n, err = recomputeIsBlockedPassForWispsInTx(ctx, tx, wispIDs)
		if err != nil {
			return total, err
		}
		changed += n
		total += changed
		if changed == 0 {
			return total, nil
		}
	}
}

// CountIsBlockedInconsistenciesInTx reports how many issue and wisp rows carry a
// stale is_blocked flag — rows a full recompute would flip. It is the read-only
// detection behind the bd doctor "Blocked State" check (bd-6dnrw.37); the
// repair is RecomputeAllIsBlockedInTx.
//
// The two share no SQL but are pinned together by the blocked-consistency
// lockstep test: a converged database counts 0, and any row this counts is one
// a recompute pass changes. The count is a single-pass lower bound — a
// corrupted parent's children are only counted on the pass after the parent is
// fixed — which is exactly what a "needs repair?" check wants: nonzero means run
// --fix, zero means consistent.
func CountIsBlockedInconsistenciesInTx(ctx context.Context, tx DBTX) (int64, error) {
	var total int64

	n, err := countRows(ctx, tx, countStaleIsBlockedSQL("issues", "i", "dependencies"))
	if err != nil {
		return 0, fmt.Errorf("count stale is_blocked issues: %w", err)
	}
	total += n

	n, err = countRows(ctx, tx, countStaleIsBlockedSQL("wisps", "w", "wisp_dependencies"))
	if err != nil {
		if isTableNotExistError(err) {
			return total, nil
		}
		return 0, fmt.Errorf("count stale is_blocked wisps: %w", err)
	}
	total += n

	return total, nil
}

// countStaleIsBlockedSQL builds a COUNT(*) of rows whose stored is_blocked
// disagrees with the dependency graph for one table (issues or wisps). The two
// OR branches mirror the mark and unmark UPDATE templates in blocked_state.go
// exactly:
//
//   - mark-eligible:   is_blocked = 0 on an open row that should be blocked.
//   - unmark-eligible: is_blocked = 1 on a row that should not be blocked
//     (closed/pinned, or no remaining reason — De Morgan of NOT EXISTS ... is
//     NOT (the shouldBeBlocked disjunction)).
//
// is_blocked is 0 or 1, so the branches are mutually exclusive and the count is
// their sum. table/alias/depTable are hardcoded constants supplied by the only
// two callers.
//
//nolint:gosec // G201: table, alias, and depTable are constant; only the constant gate SQL is interpolated.
func countStaleIsBlockedSQL(table, alias, depTable string) string {
	disjunction := shouldBeBlockedDisjunction(alias, depTable)
	return fmt.Sprintf(`
		SELECT COUNT(*) FROM %[1]s %[2]s
		WHERE
		  ( %[2]s.is_blocked = 0
		    AND %[2]s.status <> 'closed' AND %[2]s.status <> 'pinned'
		    AND ( %[3]s ) )
		  OR
		  ( %[2]s.is_blocked = 1
		    AND ( %[2]s.status = 'closed' OR %[2]s.status = 'pinned'
		          OR NOT ( %[3]s ) ) )
	`, table, alias, disjunction)
}

// shouldBeBlockedDisjunction is the OR of every reason a row should have
// is_blocked = 1: an open hard blocker (issue or wisp), a blocked parent (issue
// or wisp), or an ungated waits-for spawner. It mirrors the disjunction inside
// the mark/unmark templates in blocked_state.go; the lockstep test keeps the
// two from drifting. alias is the row's table alias, depTable its dependency
// table; the joined target tables (issues/wisps) are the same for both.
func shouldBeBlockedDisjunction(alias, depTable string) string {
	//nolint:gosec // G201: alias and depTable are constant; waitsForGateBlockedSQL is a constant template.
	return fmt.Sprintf(`
		    EXISTS (
		      SELECT 1 FROM %[2]s d
		      JOIN issues t ON t.id = d.depends_on_issue_id
		      WHERE d.issue_id = %[1]s.id
		        AND (d.type = 'blocks' OR d.type = 'conditional-blocks')
		        AND t.status <> 'closed' AND t.status <> 'pinned'
		    )
		    OR EXISTS (
		      SELECT 1 FROM %[2]s d
		      JOIN wisps t ON t.id = d.depends_on_wisp_id
		      WHERE d.issue_id = %[1]s.id
		        AND (d.type = 'blocks' OR d.type = 'conditional-blocks')
		        AND t.status <> 'closed' AND t.status <> 'pinned'
		    )
		    OR EXISTS (
		      SELECT 1 FROM %[2]s d
		      JOIN issues p ON p.id = d.depends_on_issue_id
		      WHERE d.issue_id = %[1]s.id
		        AND d.type = 'parent-child'
		        AND p.is_blocked = 1
		    )
		    OR EXISTS (
		      SELECT 1 FROM %[2]s d
		      JOIN wisps p ON p.id = d.depends_on_wisp_id
		      WHERE d.issue_id = %[1]s.id
		        AND d.type = 'parent-child'
		        AND p.is_blocked = 1
		    )
		    OR EXISTS (
		      SELECT 1 FROM %[2]s d
		      WHERE d.issue_id = %[1]s.id AND d.type = 'waits-for'
		        AND (%[3]s)
		    )
	`, alias, depTable, waitsForGateBlockedSQL)
}

// countRows runs a single COUNT(*) query and returns the scalar.
func countRows(ctx context.Context, tx DBTX, query string) (int64, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}
