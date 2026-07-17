package issueops

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// eventsSinceSafetyCap bounds an unbounded (old `since`) audit-trail scan.
// GetAllEventsSinceInTx is filtered only by created_at, so a very old `since`
// on a high-event DB would load every matching event into memory — the same
// unbounded-load class that grew a bd-list process to 128GB RSS (hq-lcu9o) and
// that beads-heq capped for SearchIssuesWithCounts. Events are smaller structs
// than issues+counts, so the risk is lower, but the load is still unbounded.
// The value matches searchCountsSafetyCap for consistency across read paths.
const eventsSinceSafetyCap = 10000

// eventsSinceTruncationWarnOnce ensures the "results truncated" warning is
// emitted at most once per process, so a broad scan does not spam stderr.
var eventsSinceTruncationWarnOnce sync.Once

// GetEventsInTx retrieves events for an issue. If limit <= 0, all events are returned.
//
//nolint:gosec // G201: table is hardcoded via WispTableRouting
func GetEventsInTx(ctx context.Context, tx *sql.Tx, issueID string, limit int) ([]*types.Event, error) {
	_, _, eventTable, _ := WispTableRouting(IsActiveWispInTx(ctx, tx, issueID))

	query := fmt.Sprintf(`
		SELECT id, issue_id, event_type, actor, old_value, new_value, comment, created_at
		FROM %s
		WHERE issue_id = ?
		ORDER BY created_at DESC
	`, eventTable)
	args := []interface{}{issueID}

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// GetAllEventsSinceInTx returns all events created after the given time,
// querying both events and wisp_events tables. The scan is hard-capped at
// eventsSinceSafetyCap rows to keep memory bounded on a high-event DB with an
// old `since` (see the constant doc). Each UNION branch is capped at cap+1 so
// real truncation is detectable; the merged set is trimmed to the cap with a
// one-time stderr warning.
//
//nolint:gosec // G201: LIMIT fragment is built from an internal int constant.
func GetAllEventsSinceInTx(ctx context.Context, tx *sql.Tx, since time.Time) ([]*types.Event, error) {
	query := fmt.Sprintf(`
		(SELECT id, issue_id, event_type, actor, old_value, new_value, comment, created_at
		FROM events
		WHERE created_at > ?
		ORDER BY created_at ASC
		LIMIT %d)
		UNION ALL
		(SELECT id, issue_id, event_type, actor, old_value, new_value, comment, created_at
		FROM wisp_events
		WHERE created_at > ?
		ORDER BY created_at ASC
		LIMIT %d)
		ORDER BY created_at ASC
	`, eventsSinceSafetyCap+1, eventsSinceSafetyCap+1)

	rows, err := tx.QueryContext(ctx, query, since, since)
	if err != nil {
		return nil, fmt.Errorf("get events since %v: %w", since, err)
	}
	defer rows.Close()

	events, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	if len(events) > eventsSinceSafetyCap {
		eventsSinceTruncationWarnOnce.Do(func() {
			fmt.Fprintf(os.Stderr,
				"Warning: event scan truncated at %d rows (safety cap); narrow the time window\n",
				eventsSinceSafetyCap)
		})
		events = events[:eventsSinceSafetyCap]
	}
	return events, nil
}

func scanEvents(rows *sql.Rows) ([]*types.Event, error) {
	var events []*types.Event
	for rows.Next() {
		var event types.Event
		var oldValue, newValue, comment sql.NullString
		if err := rows.Scan(&event.ID, &event.IssueID, &event.EventType, &event.Actor,
			&oldValue, &newValue, &comment, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if oldValue.Valid {
			event.OldValue = &oldValue.String
		}
		if newValue.Valid {
			event.NewValue = &newValue.String
		}
		if comment.Valid {
			event.Comment = &comment.String
		}
		events = append(events, &event)
	}
	return events, rows.Err()
}
