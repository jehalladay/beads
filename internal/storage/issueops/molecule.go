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

		for _, childID := range childIDs {
			info, ok := childMap[childID]
			if !ok {
				continue
			}
			stats.Total++
			switch types.Status(info.status) {
			case types.StatusClosed:
				stats.Completed++
				// beads-tcx7: track earliest/latest closure timestamps across
				// closed children so the rate/ETA computation has its two
				// endpoints. Skip rows whose closed_at is NULL (defensive: a
				// closed issue should always carry one, but old data may not).
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
			case types.StatusInProgress:
				stats.InProgress++
				if stats.CurrentStepID == "" {
					stats.CurrentStepID = childID
				}
			}
		}
	}

	return stats, nil
}
