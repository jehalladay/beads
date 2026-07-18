package storage

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// CompactionStore provides issue compaction and tiering operations.
type CompactionStore interface {
	CheckEligibility(ctx context.Context, issueID string, tier int) (bool, string, error)
	// ApplyCompaction records a compaction result and emits an EventCompacted
	// audit event attributed to actor (beads-ehtw).
	ApplyCompaction(ctx context.Context, issueID string, tier int, originalSize int, compactedSize int, commitHash string, actor string) error
	// CompactOverwrite applies the destructive content overwrite (updates) AND
	// records the compaction metadata/event ATOMICALLY in one transaction, so a
	// mid-sequence failure can't leave text compacted while compaction_level
	// stays 0 (beads-pj38). The caller must SnapshotIssue first (the recovery
	// anchor). A failure rolls back the overwrite.
	CompactOverwrite(ctx context.Context, issueID string, updates map[string]interface{}, tier int, originalSize int, commitHash string, actor string) error
	GetTier1Candidates(ctx context.Context) ([]*types.CompactionCandidate, error)
	GetTier2Candidates(ctx context.Context) ([]*types.CompactionCandidate, error)

	// SnapshotIssue archives an issue's current text content before a
	// destructive compaction overwrites it. tier is the level being compacted
	// to. Must be called before the overwrite.
	SnapshotIssue(ctx context.Context, issueID string, tier int) error
	// GetCompactionSnapshot returns the most recent archived snapshot for an
	// issue, or (nil, nil) when none exists.
	GetCompactionSnapshot(ctx context.Context, issueID string) (*types.IssueSnapshot, error)
	// RestoreFromSnapshot restores an issue's content from its most recent
	// snapshot and steps its compaction level back down. Returns the applied
	// snapshot, or (nil, nil) when none exists. Emits an EventCompacted audit
	// event attributed to actor (beads-ehtw).
	RestoreFromSnapshot(ctx context.Context, issueID string, actor string) (*types.IssueSnapshot, error)
}
