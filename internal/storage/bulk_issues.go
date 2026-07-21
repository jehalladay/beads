package storage

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// BulkIssueStore provides extended issue CRUD beyond the base Storage interface.
type BulkIssueStore interface {
	CreateIssuesWithFullOptions(ctx context.Context, issues []*types.Issue, actor string, opts BatchCreateOptions) error
	DeleteIssues(ctx context.Context, ids []string, cascade bool, force bool, dryRun bool) (*types.DeleteIssuesResult, error)
	DeleteIssuesBySourceRepo(ctx context.Context, sourceRepo string) (int, error)
	UpdateIssueID(ctx context.Context, oldID, newID string, issue *types.Issue, actor string) error
	ClaimIssue(ctx context.Context, id string, actor string) error
	ClaimReadyIssue(ctx context.Context, filter types.WorkFilter, actor string) (*types.Issue, error)
	PromoteFromEphemeral(ctx context.Context, id string, actor string) error
	// PromoteFromEphemeralWithComment promotes a wisp AND records the promotion
	// comment in a SINGLE transaction, so a comment-write failure rolls back the
	// promotion instead of leaving the bead promoted with the audit comment
	// silently dropped (beads-kdvfe). The direct path historically ran promote +
	// AddIssueComment as two independent commits; the proxied UOW path is already
	// atomic (one uw.Commit), and this brings the direct path to parity.
	PromoteFromEphemeralWithComment(ctx context.Context, id, actor, comment string) (*types.Comment, error)
	GetNextChildID(ctx context.Context, parentID string) (string, error)
}
