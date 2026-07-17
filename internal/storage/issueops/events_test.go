package issueops

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// Regression for beads-c5e: GetAllEventsSinceInTx must NOT run a LIMIT-less
// UNION over events + wisp_events. A very old `since` on a high-event DB loads
// every matching event into memory — the same unbounded-load class as the
// bd-list OOM (hq-lcu9o) and the SearchIssuesWithCounts fix (beads-heq). Each
// branch of the UNION must be hard-capped at eventsSinceSafetyCap+1 so
// truncation is detectable and memory stays bounded.
func TestGetAllEventsSinceInTxBoundsUnboundedQuery(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	// Both source branches must carry the safety cap; the +1 lets the caller
	// detect real truncation.
	mock.ExpectQuery(fmt.Sprintf(`(?s)FROM events.*LIMIT %d.*FROM wisp_events.*LIMIT %d`,
		eventsSinceSafetyCap+1, eventsSinceSafetyCap+1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "issue_id", "event_type", "actor", "old_value", "new_value", "comment", "created_at",
		}))

	if _, err := GetAllEventsSinceInTx(context.Background(), tx, time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("GetAllEventsSinceInTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

// Regression for beads-c5e: when the merged event set exceeds the safety cap,
// GetAllEventsSinceInTx must trim to the cap rather than return every row.
func TestGetAllEventsSinceInTxEnforcesSafetyCap(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	rows := sqlmock.NewRows([]string{
		"id", "issue_id", "event_type", "actor", "old_value", "new_value", "comment", "created_at",
	})
	// Return cap+5 rows: the SQL cap+1 bounds the DB load, but assert the
	// in-memory trim independently by feeding more than the cap.
	now := time.Now().UTC()
	for i := 0; i < eventsSinceSafetyCap+5; i++ {
		rows.AddRow(fmt.Sprintf("ev-%d", i), "bd-1", "created", "tester",
			nil, nil, nil, now)
	}
	mock.ExpectQuery(`(?s)FROM events.*FROM wisp_events`).WillReturnRows(rows)

	got, err := GetAllEventsSinceInTx(context.Background(), tx, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("GetAllEventsSinceInTx: %v", err)
	}
	if len(got) != eventsSinceSafetyCap {
		t.Fatalf("GetAllEventsSinceInTx returned %d events, want cap %d", len(got), eventsSinceSafetyCap)
	}
}

// A result at or under the cap must be returned untouched.
func TestGetAllEventsSinceInTxReturnsUnderCapUntouched(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	rows := sqlmock.NewRows([]string{
		"id", "issue_id", "event_type", "actor", "old_value", "new_value", "comment", "created_at",
	})
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		rows.AddRow(fmt.Sprintf("ev-%d", i), "bd-1", "created", "tester", nil, nil, nil, now)
	}
	mock.ExpectQuery(`(?s)FROM events.*FROM wisp_events`).WillReturnRows(rows)

	got, err := GetAllEventsSinceInTx(context.Background(), tx, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("GetAllEventsSinceInTx: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetAllEventsSinceInTx returned %d events, want 3", len(got))
	}
}
