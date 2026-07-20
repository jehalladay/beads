package issueops

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// TestPersistLabelsRejectsDelimiterChars is the regression test for beads-f3y1,
// the create-path coverage gap in beads-pqzx: pqzx added the ','/'\n'/'\r'
// delimiter guard to AddLabelInTx (bd tag / label add / update --add-label),
// but the CREATE-time label path (bd create --label, markdown import) persists
// via the SEPARATE PersistLabels, which only TrimSpace'd. A create-time label
// containing a newline therefore round-trips through the markdown "### Labels"
// parser (splits on ','/'\n') as MULTIPLE labels — the same corruption pqzx
// fixed, on the uncovered path. PersistLabels must reject the delimiter BEFORE
// any INSERT, matching AddLabelInTx.
func TestPersistLabelsRejectsDelimiterChars(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		label string
	}{
		{"comma", "a,b"},
		{"newline", "line1\nline2"},
		{"carriage_return", "line1\rline2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// No ExpectExec: the guard must reject BEFORE any label INSERT.
			db, mock, tx := beginMockTx(t)
			defer db.Close()

			issue := &types.Issue{ID: "bd-1", IssueType: types.TypeTask, Labels: []string{tc.label}}
			_, err := PersistLabels(context.Background(), tx, issue, "alice", "events")
			if err == nil {
				t.Fatalf("expected label %q (reserved delimiter) to be rejected by PersistLabels", tc.label)
			}
			if !strings.Contains(err.Error(), "comma") && !strings.Contains(err.Error(), "newline") &&
				!strings.Contains(err.Error(), "delimiter") {
				t.Fatalf("expected a delimiter-rejection error naming the problem, got: %v", err)
			}
			if mErr := mock.ExpectationsWereMet(); mErr != nil {
				t.Fatalf("guard must reject before any DB write, but a query ran: %v", mErr)
			}
		})
	}
}

// TestPersistLabelsAcceptsSpaceBearingLabel guards beads-ehw7: a space-bearing
// create-time label ("in progress") must still persist — the delimiter guard
// rejects ','/'\n' only, never spaces.
func TestPersistLabelsAcceptsSpaceBearingLabel(t *testing.T) {
	t.Parallel()

	db, mock, tx := beginMockTx(t)
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO labels")).
		WithArgs("bd-1", "in progress").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events")).
		WithArgs(sqlmock.AnyArg(), "bd-1", "label_added", "alice", "Added label: in progress").
		WillReturnResult(sqlmock.NewResult(1, 1))

	issue := &types.Issue{ID: "bd-1", IssueType: types.TypeTask, Labels: []string{"in progress"}}
	if _, err := PersistLabels(context.Background(), tx, issue, "alice", "events"); err != nil {
		t.Fatalf("a space-bearing create label must persist (beads-ehw7): %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected the space-bearing label INSERT to run: %v", err)
	}
}
