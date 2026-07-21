package storage

import (
	"context"
	"time"
)

// SyncResult contains the outcome of a Sync operation.
// SyncResult json tags keep `bd federation sync --json` snake_case: cmd/bd
// marshals `{"results": []*SyncResult}`, and without tags json.Marshal emitted
// every field PascalCase (Peer/StartTime/Fetched/...), violating the snake_case
// JSON contract across the whole results payload (beads-1ex1t, larger sibling of
// the ugb99 nested-SyncStatus leak; nested []Conflict is already snake_case via
// beads-8slh). The two error-typed fields are excluded (json:"-"): an `error`
// marshals to null/`{}` (no exported fields), so it carries no useful JSON — the
// non-json path surfaces them via %v.
type SyncResult struct {
	Peer              string     `json:"peer"`
	StartTime         time.Time  `json:"start_time"`
	EndTime           time.Time  `json:"end_time"`
	Fetched           bool       `json:"fetched"`
	Merged            bool       `json:"merged"`
	Pushed            bool       `json:"pushed"`
	PulledCommits     int        `json:"pulled_commits"`
	PushedCommits     int        `json:"pushed_commits"`
	Conflicts         []Conflict `json:"conflicts"`
	ConflictsResolved bool       `json:"conflicts_resolved"`
	Error             error      `json:"-"`
	PushError         error      `json:"-"` // Non-fatal push error
	// ErrorMsg carries the fatal sync error as a STRING so it survives JSON
	// marshalling. Error is `json:"-"` (beads-1ex1t: an `error` marshals to
	// null/`{}`, carrying no useful JSON), which meant `--json` dropped the
	// failure signal entirely — a failed sync rendered as {merged:false} with
	// no error (beads-o35h0). The cmd layer populates this from Error.Error()
	// so structured consumers can detect a per-peer sync failure.
	ErrorMsg string `json:"error,omitempty"`
}

// SyncStore provides sync operations with peers.
type SyncStore interface {
	Sync(ctx context.Context, peer string, strategy string) (*SyncResult, error)
	SyncStatus(ctx context.Context, peer string) (*SyncStatus, error)
}
