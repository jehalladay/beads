package issueops

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/types"
)

// ScanIssueCountsInTx populates the count fields (TotalIssues, OpenIssues,
// InProgressIssues, ClosedIssues, DeferredIssues, PinnedIssues) of stats from
// the issues table. It does NOT compute BlockedIssues or ReadyIssues — callers
// fill those in using their own blocked-ID computation strategy.
func ScanIssueCountsInTx(ctx context.Context, tx DBTX, stats *types.Statistics) error {
	// beads-2pzw: 'hooked' (a worker has attached the bead to its hook) is
	// CategoryWIP alongside in_progress — export_obsidian/show already display it
	// as in-progress. Fold it into the in_progress count so the stats summary
	// reconciles to Total (previously hooked beads were in Total but no bucket).
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN status = 'open' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status IN ('in_progress', 'hooked') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'closed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'deferred' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN pinned = 1 THEN 1 ELSE 0 END), 0)
		FROM issues
	`).Scan(
		&stats.TotalIssues,
		&stats.OpenIssues,
		&stats.InProgressIssues,
		&stats.ClosedIssues,
		&stats.DeferredIssues,
		&stats.PinnedIssues,
	); err != nil {
		return fmt.Errorf("scan issue counts: %w", err)
	}
	return nil
}

// CountEpicsEligibleForClosureInTx returns the number of open epics whose
// children are all closed (EligibleForClose). beads-13xl: bd stats declares +
// renders Statistics.EpicsEligibleForClosure ("Epics Ready to Close") and ships
// it in --json, but no GetStatistics path ever assigned it → permanent 0,
// disagreeing with `bd epic status` (which uses the same canonical helper).
// Reuses GetEpicsEligibleForClosureInTx so stats and epic-status can never drift.
func CountEpicsEligibleForClosureInTx(ctx context.Context, tx DBTX) (int, error) {
	epics, err := GetEpicsEligibleForClosureInTx(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("count epics eligible for closure: %w", err)
	}
	count := 0
	for _, e := range epics {
		if e.EligibleForClose {
			count++
		}
	}
	return count, nil
}

// ScanAverageLeadTimeInTx computes the mean lead time in hours (created_at →
// closed_at) across all closed issues with a recorded closed_at, and stores it
// in stats.AverageLeadTime. beads-13xl: this field is declared + rendered ("Avg
// Lead Time") + shipped in --json but was never assigned → permanent 0.0. When
// there are no qualifying closed issues the AVG is NULL, which COALESCE maps to
// 0.0 (matching the "only show if > 0" renderer gate).
func ScanAverageLeadTimeInTx(ctx context.Context, tx DBTX, stats *types.Statistics) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(TIMESTAMPDIFF(HOUR, created_at, closed_at)), 0)
		FROM issues
		WHERE status = 'closed' AND closed_at IS NOT NULL
	`).Scan(&stats.AverageLeadTime); err != nil {
		return fmt.Errorf("scan average lead time: %w", err)
	}
	return nil
}
