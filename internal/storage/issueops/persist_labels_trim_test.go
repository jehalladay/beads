package issueops

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// TestPersistLabelsTrimsWhitespace is a regression test for beads-4g2h:
// `bd create -l '  x  '` stored the label verbatim (PersistLabels didn't trim),
// while `bd label add`/`bd label remove` and the query side all normalize
// whitespace — so the padded label became permanently unmatchable. PersistLabels
// must trim each label and skip whitespace-only labels, matching cmd/bd/label.go.
func TestPersistLabelsTrimsWhitespace(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()

	issue := &types.Issue{
		ID:     "bd-1",
		Labels: []string{"  x  ", "   ", "x", "y\t"},
	}

	// Expected: "  x  " → "x" (inserted, changed), "   " → skipped (empty),
	// "x" → deduped against the already-seen "x", "y\t" → "y" (inserted).
	// Only two INSERTs, each with the TRIMMED value, each followed by an event.
	mock.ExpectExec("INSERT IGNORE INTO labels").
		WithArgs("bd-1", "x").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO events").
		WithArgs(sqlmock.AnyArg(), "bd-1", types.EventLabelAdded, "actor", "Added label: x").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT IGNORE INTO labels").
		WithArgs("bd-1", "y").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO events").
		WithArgs(sqlmock.AnyArg(), "bd-1", types.EventLabelAdded, "actor", "Added label: y").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if _, err := PersistLabels(ctx, tx, issue, "actor", "events"); err != nil {
		t.Fatalf("PersistLabels: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (label not trimmed / empty not skipped? beads-4g2h): %v", err)
	}
}
