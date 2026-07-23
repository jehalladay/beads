package issueops

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// GetMoleculeProgressInTx returns progress stats for a molecule within an
// existing transaction. Routes to the correct table (issues/wisps) automatically.
//
//nolint:gosec // G201: table names come from WispTableRouting (hardcoded constants)
func GetMoleculeProgressInTx(ctx context.Context, tx *sql.Tx, moleculeID string) (*types.MoleculeProgressStats, error) {
	stats := &types.MoleculeProgressStats{
		MoleculeID: moleculeID,
	}

	isWisp := IsActiveWispInTx(ctx, tx, moleculeID)
	issueTable, _, _, _ := WispTableRouting(isWisp)

	// Get molecule title.
	var title sql.NullString
	err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT title FROM %s WHERE id = ?", issueTable), moleculeID).Scan(&title)
	if err == nil && title.Valid {
		stats.MoleculeTitle = title.String
	}

	// Step 1: Get ALL descendant IDs (recursive), not just direct children.
	// beads-1s2q8: the previous one-hop parent-child query counted only direct
	// children, so a molecule whose direct children were all closed reported
	// 100% even while nested grandchildren remained open — diverging from the
	// RECURSIVE accounting mol current/mol show use (loadTemplateSubgraph ->
	// loadDescendants) and from autoclose's descendant walk. Reuse the SAME
	// recursive descendant traversal the ready-work path uses (blocked.go
	// GetDescendantIDsInTx, maxDepth=0 = unbounded, cycle-safe, spans
	// issues+wisps) rather than minting a parallel one-level walk.
	childIDs, err := GetDescendantIDsInTx(ctx, tx, moleculeID, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get molecule descendants: %w", err)
	}

	// Step 2: Batch-fetch status for all children.
	// Children of a wisp molecule are also wisps, so use the same table.
	// beads-tcx7: also fetch closed_at so FirstClosed/LastClosed can be
	// populated — the rate/ETA feature (mol_progress.go rate_per_hour/eta_hours
	// + human "Rate: ~N steps/hour") was dead because no producer ever assigned
	// those two fields, so the FirstClosed!=nil && LastClosed!=nil guard was
	// never true.
	if len(childIDs) > 0 {
		type childInfo struct {
			status   string
			closedAt sql.NullTime
		}
		childMap := make(map[string]childInfo)
		for start := 0; start < len(childIDs); start += queryBatchSize {
			end := start + queryBatchSize
			if end > len(childIDs) {
				end = len(childIDs)
			}
			batch := childIDs[start:end]
			placeholders := make([]string, len(batch))
			args := make([]any, len(batch))
			for i, id := range batch {
				placeholders[i] = "?"
				args[i] = id
			}
			inClause := strings.Join(placeholders, ",")

			query := fmt.Sprintf("SELECT id, status, closed_at FROM %s WHERE id IN (%s)", issueTable, inClause)
			statusRows, err := tx.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("failed to batch-fetch child statuses: %w", err)
			}
			for statusRows.Next() {
				var id, status string
				var closedAt sql.NullTime
				if err := statusRows.Scan(&id, &status, &closedAt); err != nil {
					_ = statusRows.Close()
					return nil, fmt.Errorf("get molecule progress: scan status: %w", err)
				}
				childMap[id] = childInfo{status: status, closedAt: closedAt}
			}
			if err := statusRows.Err(); err != nil {
				_ = statusRows.Close()
				return nil, fmt.Errorf("get molecule progress: iterate statuses: %w", err)
			}
			_ = statusRows.Close()
		}

		// beads-bobpm: a custom done-category status is a terminal "done"
		// outcome, so a step in such a status must count toward Completed
		// exactly like a literal-closed step — matching the cmd-side
		// getMoleculeProgress (beads-x463g), autoclose, and bd ready/count/list.
		// Resolve the done-category names once; a nil/empty set (no config or a
		// resolution error) leaves counting byte-identical to pre-bobpm (only
		// types.StatusClosed completes). Degraded-safe: a config read error just
		// yields an empty done-set rather than failing progress accounting.
		doneStatusNames := map[string]bool{}
		if detailed, cerr := ResolveCustomStatusesDetailedInTx(ctx, tx); cerr == nil {
			for _, cs := range detailed {
				if cs.Category == types.CategoryDone {
					doneStatusNames[cs.Name] = true
				}
			}
		}

		for _, childID := range childIDs {
			info, ok := childMap[childID]
			if !ok {
				continue
			}
			stats.Total++
			switch {
			case types.Status(info.status) == types.StatusClosed || doneStatusNames[info.status]:
				stats.Completed++
				// beads-tcx7: track earliest/latest closure timestamps across
				// completed children so the rate/ETA computation has its two
				// endpoints. Skip rows whose closed_at is NULL — defensive for
				// literal-closed rows, and expected for a done-category step
				// (a non-'closed' status typically carries no closed_at), which
				// still counts toward Completed but simply cannot anchor a rate
				// endpoint. (beads-bobpm.)
				if info.closedAt.Valid {
					t := info.closedAt.Time
					if stats.FirstClosed == nil || t.Before(*stats.FirstClosed) {
						ft := t
						stats.FirstClosed = &ft
					}
					if stats.LastClosed == nil || t.After(*stats.LastClosed) {
						lt := t
						stats.LastClosed = &lt
					}
				}
			case types.Status(info.status) == types.StatusInProgress:
				stats.InProgress++
				if stats.CurrentStepID == "" {
					stats.CurrentStepID = childID
				}
			}
		}
	}

	return stats, nil
}
