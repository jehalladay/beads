package issueops

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

type ReopenResult struct {
	IsWisp      bool
	AlreadyOpen bool
}

//nolint:gosec // G201: table names come from WispTableRouting (hardcoded constants)
func ReopenIssueInTx(ctx context.Context, tx DBTX, id, reason, actor string) (*ReopenResult, error) {
	isWisp := IsActiveWispInTx(ctx, tx, id)
	issueTable, _, eventTable, _ := WispTableRouting(isWisp)

	var affectedIssues, affectedWisps []string
	var aerr error
	if isWisp {
		affectedIssues, affectedWisps, aerr = AffectedByStatusChangeForWispInTx(ctx, tx, id)
	} else {
		affectedIssues, affectedWisps, aerr = AffectedByStatusChangeInTx(ctx, tx, id)
	}
	if aerr != nil {
		return nil, fmt.Errorf("affected by reopen for %s: %w", id, aerr)
	}

	now := time.Now().UTC()

	// beads-7us7e: a custom done-category status (e.g. "resolved:done") is a
	// terminal/complete outcome everywhere else in the x463g class (views exclude
	// it; is_blocked/ready treat a done-category blocker as unblocking; molecule
	// progress + the close guard + ship count it complete). reopen's purpose is
	// terminal->open, so it MUST apply to a done-category status exactly as to
	// literal-closed — refusing it IS the divergence (PM-blessed). Widen the CAS's
	// terminal-status match from `status = 'closed'` to `status IN (closed, <done
	// names>)`. FROZEN is excluded (parked != done). ManageClosedAt shows a
	// done-category status carries closed_at IS NULL, so clearing the close
	// columns here is invariant-safe (status=closed <=> closed_at!=NULL holds).
	// Degraded-safe: an empty done-set reduces the IN clause to a single
	// 'closed' bind = byte-identical to the pre-7us7e behavior.
	terminalStatuses := []interface{}{string(types.StatusClosed)}
	if detailed, cerr := ResolveCustomStatusesDetailedInTx(ctx, tx); cerr == nil {
		for _, cs := range detailed {
			if cs.Category == types.CategoryDone {
				terminalStatuses = append(terminalStatuses, cs.Name)
			}
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(terminalStatuses)), ",")

	updateArgs := append([]interface{}{types.StatusOpen, now, id}, terminalStatuses...)
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET status = ?, closed_at = NULL, close_reason = '', closed_by_session = '', defer_until = NULL, updated_at = ?
		WHERE id = ? AND status IN (%s)
	`, issueTable, placeholders), updateArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to reopen issue: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var status string
		qerr := tx.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT status FROM %s WHERE id = ?`, issueTable), id,
		).Scan(&status)
		if qerr == sql.ErrNoRows {
			return nil, fmt.Errorf("issue not found: %s", id)
		}
		if qerr != nil {
			return nil, fmt.Errorf("failed to check issue existence: %w", qerr)
		}
		// Not in a terminal status (open / in_progress / blocked / deferred):
		// an idempotent no-op, matching the pre-7us7e already-open handling. A
		// terminal status here (in the IN set) with 0 rows would be an impossible
		// concurrent-write race, preserved as an error (was `status != closed`).
		isTerminal := false
		for _, s := range terminalStatuses {
			if status == s.(string) {
				isTerminal = true
				break
			}
		}
		if !isTerminal {
			return &ReopenResult{IsWisp: isWisp, AlreadyOpen: true}, nil
		}
		return nil, fmt.Errorf("failed to reopen issue: %s", id)
	}

	if err := RecordEventInTable(ctx, tx, eventTable, id, types.EventReopened, actor, reason); err != nil {
		return nil, fmt.Errorf("failed to record event: %w", err)
	}

	if reason != "" {
		// beads-bimd0: write the reopen reason to the COMMENTS table via
		// AddIssueCommentInTx, NOT the events table via AddCommentEventInTx.
		// bd show / bd comments read GetIssueComments (comments table); an
		// EventCommented row in `events` is surfaced by no read path, so the
		// documented "recorded as a comment" reason was silently invisible on
		// every reopen path (same class as beads-9l1it, which moved the promote
		// reason off the events table for the identical reason). The
		// EventReopened row above still carries the reason for the audit trail.
		if _, err := AddIssueCommentInTx(ctx, tx, id, actor, reason); err != nil {
			return nil, fmt.Errorf("failed to add reopen comment: %w", err)
		}
	}

	if err := RecomputeIsBlockedInTx(ctx, tx, affectedIssues, affectedWisps); err != nil {
		return nil, fmt.Errorf("recompute is_blocked after reopen for %s: %w", id, err)
	}

	return &ReopenResult{IsWisp: isWisp}, nil
}
