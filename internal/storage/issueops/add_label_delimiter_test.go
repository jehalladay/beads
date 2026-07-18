package issueops

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestAddLabelInTxRejectsDelimiterChars is the regression test for beads-pqzx:
// a label containing ',' or a newline was stored verbatim (only TrimSpace ran),
// but the markdown "### Labels" round-trip splits that section on BOTH ',' and
// '\n' (parseLabels, cmd/bd/markdown.go), so a single stored label like
// "a,b" / "line1\nline2" RE-IMPORTS as two labels — round-trip identity
// corruption, the native leg of the SCM label-delimiter-collision class
// (pcz2/ADO ';', i8gh/Notion ',', xcbd/Jira). AddLabelInTx is the single
// live-path chokepoint, so the guard here covers tag + label add + every other
// AddLabel caller. The label must be rejected BEFORE any INSERT.
func TestAddLabelInTxRejectsDelimiterChars(t *testing.T) {
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
			// No ExpectExec: the guard must reject BEFORE any INSERT.
			_, mock, tx := beginMockTx(t)
			err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", tc.label, "alice")
			if err == nil {
				t.Fatalf("expected label %q (contains a reserved delimiter) to be rejected", tc.label)
			}
			// Rejected by the delimiter guard, not by an unexpected-exec mock
			// error (which would pass on unfixed code that attempts the INSERT).
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

// TestAddLabelInTxAcceptsSpaceBearingLabel guards against a regression of
// beads-ehw7: labels may legitimately contain SPACES ("in progress"), which the
// markdown parseLabels splits on ',' / '\n' only (never spaces). The delimiter
// guard must reject ',' / '\n' but must NOT reject a space-bearing label.
func TestAddLabelInTxAcceptsSpaceBearingLabel(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO labels (issue_id, label) VALUES (?, ?)")).
		WithArgs("bd-1", "in progress").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
		WithArgs(sqlmock.AnyArg(), "bd-1", "label_added", "alice", "Added label: in progress").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "in progress", "alice"); err != nil {
		t.Fatalf("a space-bearing label must be accepted (beads-ehw7): %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected the INSERT to run for a space-bearing label: %v", err)
	}
}
