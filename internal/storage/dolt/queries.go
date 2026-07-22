package dolt

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// SearchIssues finds issues matching query and filters.
// Delegates to issueops.SearchIssuesInTx for shared query logic.
func (s *DoltStore) SearchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	var result []*types.Issue
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.SearchIssuesInTx(ctx, tx, query, filter)
		return err
	})
	return result, err
}

func (s *DoltStore) SearchIssuesWithCounts(ctx context.Context, query string, filter types.IssueFilter) ([]*types.IssueWithCounts, error) {
	var result []*types.IssueWithCounts
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.SearchIssuesWithCountsInTx(ctx, tx, query, filter)
		return err
	})
	return result, err
}

func (s *DoltStore) GetReadyWork(ctx context.Context, filter types.WorkFilter) ([]*types.Issue, error) {
	var result []*types.Issue
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetReadyWorkInTx(ctx, tx, filter)
		return err
	})
	return result, err
}

func (s *DoltStore) GetReadyWorkWithCounts(ctx context.Context, filter types.WorkFilter) ([]*types.IssueWithCounts, error) {
	var result []*types.IssueWithCounts
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetReadyWorkWithCountsInTx(ctx, tx, filter)
		return err
	})
	return result, err
}

func (s *DoltStore) GetBlockedIssues(ctx context.Context, filter types.WorkFilter) ([]*types.BlockedIssue, error) {
	var result []*types.BlockedIssue
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetBlockedIssuesInTx(ctx, tx, filter)
		return err
	})
	return result, err
}

// GetEpicsEligibleForClosure returns epics whose children are all closed
func (s *DoltStore) GetEpicsEligibleForClosure(ctx context.Context) ([]*types.EpicStatus, error) {
	var result []*types.EpicStatus
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetEpicsEligibleForClosureInTx(ctx, tx)
		return err
	})
	return result, err
}

// GetStaleIssues returns issues that haven't been updated recently
func (s *DoltStore) GetStaleIssues(ctx context.Context, filter types.StaleFilter) ([]*types.Issue, error) {
	var result []*types.Issue
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetStaleIssuesInTx(ctx, tx, filter)
		return err
	})
	return result, err
}

// GetStatistics returns summary statistics
func (s *DoltStore) GetStatistics(ctx context.Context) (*types.Statistics, error) {
	stats := &types.Statistics{}

	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		return issueops.ScanIssueCountsInTx(ctx, tx, stats)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get statistics: %w", err)
	}

	var blockedCount int
	if err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM issues
			WHERE is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'
		`).Scan(&blockedCount)
	}); err != nil {
		return nil, fmt.Errorf("failed to count blocked issues: %w", err)
	}
	stats.BlockedIssues = blockedCount

	// beads-phoh: count ready work through the shared bd-ready predicate
	// (type/label identity exclusions included) instead of the naive
	// OpenIssues-blocked subtraction, so `bd stats` ready_issues matches
	// `bd ready` and does not overcount unblocked identity beads.
	if err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		readyCount, rerr := issueops.CountReadyWorkInTx(ctx, tx, issueops.StatsReadyWorkFilter())
		if rerr != nil {
			return rerr
		}
		stats.ReadyIssues = readyCount
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to count ready issues: %w", err)
	}

	// beads-13xl: populate the two Statistics fields that were declared +
	// rendered but never assigned (permanent 0), so `bd stats` agrees with
	// `bd epic status` and stops silently lying about lead time.
	if err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		epicCount, eerr := issueops.CountEpicsEligibleForClosureInTx(ctx, tx)
		if eerr != nil {
			return eerr
		}
		stats.EpicsEligibleForClosure = epicCount
		return issueops.ScanAverageLeadTimeInTx(ctx, tx, stats)
	}); err != nil {
		return nil, fmt.Errorf("failed to compute extended statistics: %w", err)
	}

	return stats, nil
}

// GetMoleculeProgress returns progress stats for a molecule.
//
// beads-1s2q8: delegates to issueops.GetMoleculeProgressInTx (the same shared
// logic EmbeddedDoltStore uses) so both backends count ALL recursive
// descendants, not just direct children. The prior inline body here duplicated
// the counting loop AND the one-hop direct-children query, so a molecule whose
// direct children were all closed reported a false 100% while nested
// grandchildren stayed open — diverging from the recursive accounting mol
// current/mol show/autoclose use. Delegating removes the duplicate bug at its
// root (single source of truth, matching the other query methods above).
func (s *DoltStore) GetMoleculeProgress(ctx context.Context, moleculeID string) (*types.MoleculeProgressStats, error) {
	var result *types.MoleculeProgressStats
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetMoleculeProgressInTx(ctx, tx, moleculeID)
		return err
	})
	return result, err
}

// GetMoleculeLastActivity returns the most recent activity timestamp for a molecule.
func (s *DoltStore) GetMoleculeLastActivity(ctx context.Context, moleculeID string) (*types.MoleculeLastActivity, error) {
	var result *types.MoleculeLastActivity
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetMoleculeLastActivityInTx(ctx, tx, moleculeID)
		return err
	})
	return result, err
}

// GetNextChildID returns the next available child ID for a parent.
// Delegates SQL work to issueops.GetNextChildIDTx.
func (s *DoltStore) GetNextChildID(ctx context.Context, parentID string) (string, error) {
	var childID string
	err := s.withRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		childID, err = issueops.GetNextChildIDTx(ctx, tx, parentID)
		return err
	})
	return childID, err
}
