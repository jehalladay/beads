//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

func (s *EmbeddedDoltStore) GetStatistics(ctx context.Context) (*types.Statistics, error) {
	stats := &types.Statistics{}
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		if err := issueops.ScanIssueCountsInTx(ctx, tx, stats); err != nil {
			return err
		}

		var blockedCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM issues
			WHERE is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'
		`).Scan(&blockedCount); err != nil {
			return err
		}
		stats.BlockedIssues = blockedCount
		// beads-phoh: count ready work through the shared bd-ready predicate
		// (identity type/label exclusions included) so `bd stats` ready_issues
		// matches `bd ready` instead of overcounting unblocked identity beads.
		readyCount, rerr := issueops.CountReadyWorkInTx(ctx, tx, issueops.StatsReadyWorkFilter())
		if rerr != nil {
			return rerr
		}
		stats.ReadyIssues = readyCount

		// beads-13xl: populate the two Statistics fields that were declared +
		// rendered but never assigned (permanent 0), so `bd stats` agrees with
		// `bd epic status` and stops silently lying about lead time.
		epicCount, eerr := issueops.CountEpicsEligibleForClosureInTx(ctx, tx)
		if eerr != nil {
			return eerr
		}
		stats.EpicsEligibleForClosure = epicCount
		if lerr := issueops.ScanAverageLeadTimeInTx(ctx, tx, stats); lerr != nil {
			return lerr
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("embeddeddolt: get statistics: %w", err)
	}
	return stats, nil
}
