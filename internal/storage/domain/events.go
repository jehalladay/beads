package domain

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

type Event struct {
	IssueID  string
	Type     types.EventType
	Actor    string
	OldValue string
	NewValue string
	// Comment is the human-readable audit line (the events.comment column). The
	// direct issueops path records label/dep/etc. events with a descriptive
	// comment (e.g. "Added label: bug"); mirror it here so the proxied twin
	// produces the same audit row shape (beads-6p27f). Empty Comment is written
	// as SQL NULL, matching a direct path that leaves comment unset.
	Comment string
}

type RecordEventOpts struct {
	UseWispsTable bool
}

type EventsSQLRepository interface {
	Record(ctx context.Context, evt Event, opts RecordEventOpts) error
	DeleteAllForIDs(ctx context.Context, ids []string, opts RecordEventOpts) (int, error)
	CountAllForIDs(ctx context.Context, ids []string, opts RecordEventOpts) (int, error)
}
