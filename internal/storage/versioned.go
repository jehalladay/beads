// Package storage provides shared types for issue storage.
package storage

import (
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// HistoryEntry represents an issue at a specific point in history.
type HistoryEntry struct {
	CommitHash string       `json:"commit_hash"` // The commit hash at this point
	Committer  string       `json:"committer"`   // Who made the commit
	CommitDate time.Time    `json:"commit_date"` // When the commit was made
	Issue      *types.Issue `json:"issue"`       // The issue state at that commit
}

// DiffEntry represents a change between two commits.
type DiffEntry struct {
	// beads-8slh: without json tags these serialize the Go field names verbatim
	// (PascalCase IssueID/DiffType/...), so `bd diff --json` emitted PascalCase
	// keys while the sibling HistoryEntry (and the nested types.Issue) are
	// snake_case. Tag them to match.
	IssueID  string       `json:"issue_id"`  // The ID of the affected issue
	DiffType string       `json:"diff_type"` // "added", "modified", or "removed"
	OldValue *types.Issue `json:"old_value"` // State before (nil for "added")
	NewValue *types.Issue `json:"new_value"` // State after (nil for "removed")
}

// Conflict represents a merge conflict.
type Conflict struct {
	// beads-8slh: same casing leak as DiffEntry — `bd federation sync --json`
	// marshals SyncResult.Conflicts, so these wrapper fields need snake_case tags.
	IssueID     string      `json:"issue_id"`     // The ID of the conflicting issue
	Field       string      `json:"field"`        // Which field has the conflict (empty for table-level)
	OursValue   interface{} `json:"ours_value"`   // Value on current branch
	TheirsValue interface{} `json:"theirs_value"` // Value on merged branch
}

// RemoteInfo describes a configured remote.
type RemoteInfo struct {
	Name string `json:"name"` // Remote name (e.g., "town-beta")
	URL  string `json:"url"`  // Remote URL (e.g., "dolthub://org/repo")
}

// SyncStatus describes the synchronization state with a peer.
type SyncStatus struct {
	Peer         string    // Peer name
	LastSync     time.Time // When last synced
	LocalAhead   int       // Commits ahead of peer
	LocalBehind  int       // Commits behind peer
	HasConflicts bool      // Whether there are unresolved conflicts
}

// FederationPeer represents a remote peer with authentication credentials.
// Used for peer-to-peer Dolt remotes between workspaces with SQL user auth.
type FederationPeer struct {
	Name        string     // Unique name for this peer (used as remote name)
	RemoteURL   string     // Dolt remote URL (e.g., http://host:port/org/db)
	Username    string     // SQL username for authentication
	Password    string     // Password (decrypted, not stored directly)
	Sovereignty string     // Sovereignty tier: T1, T2, T3, T4
	LastSync    *time.Time // Last successful sync time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
