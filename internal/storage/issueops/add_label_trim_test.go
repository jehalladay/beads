package issueops

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestAddLabelInTxTrimsWhitespace is the regression test for beads-13zc: the
// update-path (bd update --add-label / --set-labels -> applyLabelUpdates ->
// st.AddLabel -> AddLabelInTx) INSERTed the label VERBATIM, so a padded label
// like "  bug  " was stored with its surrounding whitespace. The query/filter
// side normalizes input via utils.NormalizeLabels (TrimSpace + drop-empty), so
// a padded stored label is PERMANENTLY UNMATCHABLE (bd list -l bug = 0 rows).
//
// AddLabelInTx is the single live-path chokepoint for both the dolt and
// embeddeddolt stores (create -l is fixed separately in create.go's persist
// loop by beads-4g2h). Trimming here retires the create-vs-update asymmetry for
// every AddLabel caller. The INSERT must receive the TRIMMED value.
func TestAddLabelInTxTrimsWhitespace(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	// The INSERT must carry the trimmed "bug", not the padded "  bug  ".
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO labels (issue_id, label) VALUES (?, ?)")).
		WithArgs("bd-1", "bug").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// The recorded event comment must also use the trimmed value.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
		WithArgs(sqlmock.AnyArg(), "bd-1", "label_added", "alice", "Added label: bug").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "  bug  ", "alice"); err != nil {
		t.Fatalf("AddLabelInTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected the INSERT to receive the trimmed label: %v", err)
	}
}

// TestAddLabelInTxRejectsWhitespaceOnly verifies a label that is only
// whitespace ("   ") trims to empty and is rejected before any write — mirrors
// the CLI 'bd label add' guard (label.go: TrimSpace then reject empty) so the
// storage chokepoint can't persist an empty/whitespace label from any caller.
func TestAddLabelInTxRejectsWhitespaceOnly(t *testing.T) {
	t.Parallel()

	// No ExpectExec: the guard must reject BEFORE any INSERT.
	_, mock, tx := beginMockTx(t)
	err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "   ", "alice")
	if err == nil {
		t.Fatal("expected a whitespace-only label to be rejected")
	}
	// Must be rejected by the empty-after-trim guard, NOT by an unexpected-exec
	// error — otherwise this test would pass on unfixed code (which attempts the
	// INSERT and the mock rejects the unexpected call). Name the reason.
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected an empty-label rejection, got: %v", err)
	}
	if mErr := mock.ExpectationsWereMet(); mErr != nil {
		t.Fatalf("guard must reject before any DB write, but a query ran: %v", mErr)
	}
}

// TestRemoveLabelInTxTrimsWhitespace verifies the remove path trims too, so
// 'bd update ID --remove-label "  bug  "' can match a label stored as "bug".
func TestRemoveLabelInTxTrimsWhitespace(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM labels WHERE issue_id = ? AND LOWER(label) = ?")).
		WithArgs("bd-1", "bug").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
		WithArgs(sqlmock.AnyArg(), "bd-1", "label_removed", "alice", "Removed label: bug").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := RemoveLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "  bug  ", "alice"); err != nil {
		t.Fatalf("RemoveLabelInTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected the DELETE to receive the trimmed label: %v", err)
	}
}
