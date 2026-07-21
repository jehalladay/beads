//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

func (s *EmbeddedDoltStore) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	return s.AddDependencyWithOptions(ctx, dep, actor, storage.DependencyAddOptions{})
}

// AddDependencyWithOptions adds a single dependency edge honoring opts (notably
// opts.SkipCycleCheck for `bd dep add --no-cycle-check`, beads-2f1ly). Self-loop
// rejection stays unconditional inside AddDependencyInTx (beads-jg2s).
func (s *EmbeddedDoltStore) AddDependencyWithOptions(ctx context.Context, dep *types.Dependency, actor string, opts storage.DependencyAddOptions) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.AddDependencyInTx(ctx, tx, dep, actor, issueops.AddDependencyOpts{
			IsCrossPrefix:  types.ExtractPrefix(dep.IssueID) != types.ExtractPrefix(dep.DependsOnID),
			SkipCycleCheck: opts.SkipCycleCheck,
		})
	})
}

// LinkAndClose adds a dependency edge AND closes dep.IssueID in ONE transaction
// so they can't split into an inconsistent state (beads-njnw; same class as
// compaction's overwrite+mark, beads-pj38). Used by bd duplicate / bd supersede.
func (s *EmbeddedDoltStore) LinkAndClose(ctx context.Context, dep *types.Dependency, actor string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.LinkAndCloseInTx(ctx, tx, dep, actor, issueops.AddDependencyOpts{
			IsCrossPrefix: types.ExtractPrefix(dep.IssueID) != types.ExtractPrefix(dep.DependsOnID),
		})
	})
}

// RemoveDependency removes a dependency between two issues.
func (s *EmbeddedDoltStore) RemoveDependency(ctx context.Context, issueID, dependsOnID string, actor string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.RemoveDependencyInTx(ctx, tx, issueID, dependsOnID, actor)
	})
}

// GetIssuesByIDs retrieves multiple issues by ID.
func (s *EmbeddedDoltStore) GetIssuesByIDs(ctx context.Context, ids []string) ([]*types.Issue, error) {
	var result []*types.Issue
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetIssuesByIDsInTx(ctx, tx, ids, nil)
		return err
	})
	return result, err
}

// GetDependenciesWithMetadata returns issues that the given issue depends on,
// along with the dependency type.
func (s *EmbeddedDoltStore) GetDependenciesWithMetadata(ctx context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	var result []*types.IssueWithDependencyMetadata
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetDependenciesWithMetadataInTx(ctx, tx, issueID)
		return err
	})
	return result, err
}

// GetDependentsWithMetadata returns issues that depend on the given issue,
// along with the dependency type.
func (s *EmbeddedDoltStore) GetDependentsWithMetadata(ctx context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	var result []*types.IssueWithDependencyMetadata
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetDependentsWithMetadataInTx(ctx, tx, issueID)
		return err
	})
	return result, err
}

// DetectCycles finds dependency cycles across both permanent and wisp dependencies.
func (s *EmbeddedDoltStore) DetectCycles(ctx context.Context) ([][]*types.Issue, error) {
	var result [][]*types.Issue
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.DetectCyclesInTx(ctx, tx)
		return err
	})
	return result, err
}
