package issueops

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetLabelsForIssuesFromTableInTx(t *testing.T) {
	t.Parallel()

	t.Run("empty ids returns empty map without querying", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		got, err := GetLabelsForIssuesFromTableInTx(context.Background(), tx, "labels", nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want empty map", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unexpected queries: %v", err)
		}
	})

	t.Run("accumulates labels per issue id", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta(
			"SELECT issue_id, label FROM labels WHERE issue_id IN (?,?) ORDER BY issue_id, label")).
			WithArgs("bd-1", "bd-2").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}).
				AddRow("bd-1", "bug").
				AddRow("bd-1", "p1").
				AddRow("bd-2", "chore"))
		got, err := GetLabelsForIssuesFromTableInTx(context.Background(), tx, "labels", []string{"bd-1", "bd-2"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got["bd-1"]) != 2 || got["bd-1"][0] != "bug" || got["bd-1"][1] != "p1" {
			t.Fatalf("bd-1 labels = %v, want [bug p1]", got["bd-1"])
		}
		if len(got["bd-2"]) != 1 || got["bd-2"][0] != "chore" {
			t.Fatalf("bd-2 labels = %v, want [chore]", got["bd-2"])
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT issue_id, label FROM labels").
			WithArgs("bd-1").WillReturnError(errors.New("boom"))
		_, err := GetLabelsForIssuesFromTableInTx(context.Background(), tx, "labels", []string{"bd-1"})
		if err == nil {
			t.Fatal("expected wrapped query error")
		}
	})
}

func TestAddLabelInTx(t *testing.T) {
	t.Parallel()

	t.Run("inserts label then records the label_added event", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO labels (issue_id, label) VALUES (?, ?)")).
			WithArgs("bd-1", "bug").
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
			WithArgs(sqlmock.AnyArg(), "bd-1", "label_added", "alice", "Added label: bug").
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "bug", "alice"); err != nil {
			t.Fatalf("AddLabelInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("no-op insert (already present) writes no event", func(t *testing.T) {
		t.Parallel()
		// INSERT IGNORE on a duplicate label affects 0 rows; recording a
		// label_added event for an addition that never happened pollutes the
		// audit trail (beads-usz1). Expect ONLY the INSERT IGNORE, no event.
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO labels (issue_id, label) VALUES (?, ?)")).
			WithArgs("bd-1", "bug").
			WillReturnResult(sqlmock.NewResult(0, 0))
		if err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "bug", "alice"); err != nil {
			t.Fatalf("AddLabelInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("expected no event write on no-op insert: %v", err)
		}
	})

	t.Run("label insert error is wrapped (no event write)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("INSERT IGNORE INTO labels").WillReturnError(errors.New("boom"))
		if err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "bug", "alice"); err == nil {
			t.Fatal("expected wrapped label-insert error")
		}
	})

	t.Run("over-length label is rejected before any write (beads-ho89)", func(t *testing.T) {
		t.Parallel()
		// No ExpectExec: the guard must reject BEFORE the INSERT, so a raw
		// "Error 1105 too large for column" can never leak. A 256-char label
		// exceeds the VARCHAR(255) column width.
		_, mock, tx := beginMockTx(t)
		tooLong := strings.Repeat("x", maxLabelLen+1)
		err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", tooLong, "alice")
		if err == nil {
			t.Fatal("expected an over-length label to be rejected")
		}
		if !strings.Contains(err.Error(), "255 characters or less") {
			t.Fatalf("error should name the limit, got: %v", err)
		}
		// Assert no INSERT/event write was attempted.
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("guard must reject before any DB write, but a query ran: %v", err)
		}
	})

	t.Run("label at exactly the max length is allowed", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		atMax := strings.Repeat("y", maxLabelLen)
		mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO labels (issue_id, label) VALUES (?, ?)")).
			WithArgs("bd-1", atMax).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
			WithArgs(sqlmock.AnyArg(), "bd-1", "label_added", "alice", "Added label: "+atMax).
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", atMax, "alice"); err != nil {
			t.Fatalf("label at exactly max length must be allowed: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("event insert error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("INSERT IGNORE INTO labels").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO events").WillReturnError(errors.New("boom"))
		if err := AddLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "bug", "alice"); err == nil {
			t.Fatal("expected wrapped event-insert error")
		}
	})
}

func TestSetLabelsInTx(t *testing.T) {
	t.Parallel()

	// SetLabelsInTx is the canonical diff-based label-set replace lifted to the
	// shared in-tx seam (beads-idvy). It must read the current set once, then
	// remove ONLY current-not-desired and add ONLY desired-not-present — leaving
	// unchanged labels untouched (no churn event) and never doing remove-all-add-all.
	// Explicit table names ("labels"/"events") skip the wisp-routing probe.

	t.Run("overlap removes only current-not-desired and adds only desired-not-present", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		// current {a,b} -> desired {b,c}: read current, remove a, add c; b untouched.
		mock.ExpectQuery(regexp.QuoteMeta("SELECT label FROM labels WHERE issue_id = ? ORDER BY label")).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"label"}).AddRow("a").AddRow("b"))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM labels WHERE issue_id = ? AND LOWER(label) = ?")).
			WithArgs("bd-1", "a").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
			WithArgs(sqlmock.AnyArg(), "bd-1", "label_removed", "alice", "Removed label: a").
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO labels (issue_id, label) VALUES (?, ?)")).
			WithArgs("bd-1", "c").
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
			WithArgs(sqlmock.AnyArg(), "bd-1", "label_added", "alice", "Added label: c").
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := SetLabelsInTx(context.Background(), tx, "labels", "events", "bd-1", []string{"b", "c"}, "alice"); err != nil {
			t.Fatalf("SetLabelsInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations (churn or wrong diff): %v", err)
		}
	})

	t.Run("identical set is a pure read no-op (no writes)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		// desired == current {a,b}: only the read runs; no DELETE/INSERT/event.
		mock.ExpectQuery(regexp.QuoteMeta("SELECT label FROM labels WHERE issue_id = ? ORDER BY label")).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"label"}).AddRow("a").AddRow("b"))
		if err := SetLabelsInTx(context.Background(), tx, "labels", "events", "bd-1", []string{"a", "b"}, "alice"); err != nil {
			t.Fatalf("SetLabelsInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("identical set must not write: %v", err)
		}
	})

	t.Run("empty desired set removes every current label", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT label FROM labels WHERE issue_id = ? ORDER BY label")).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"label"}).AddRow("a"))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM labels WHERE issue_id = ? AND LOWER(label) = ?")).
			WithArgs("bd-1", "a").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
			WithArgs(sqlmock.AnyArg(), "bd-1", "label_removed", "alice", "Removed label: a").
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := SetLabelsInTx(context.Background(), tx, "labels", "events", "bd-1", nil, "alice"); err != nil {
			t.Fatalf("SetLabelsInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("whitespace-only desired entries are skipped (not added)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		// desired {"  ", "c"} against empty current: "  " trimmed away, only c added.
		mock.ExpectQuery(regexp.QuoteMeta("SELECT label FROM labels WHERE issue_id = ? ORDER BY label")).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"label"}))
		mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO labels (issue_id, label) VALUES (?, ?)")).
			WithArgs("bd-1", "c").
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
			WithArgs(sqlmock.AnyArg(), "bd-1", "label_added", "alice", "Added label: c").
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := SetLabelsInTx(context.Background(), tx, "labels", "events", "bd-1", []string{"  ", "c"}, "alice"); err != nil {
			t.Fatalf("SetLabelsInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("read error aborts before any write", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT label FROM labels WHERE issue_id = ? ORDER BY label")).
			WithArgs("bd-1").
			WillReturnError(errors.New("boom"))
		if err := SetLabelsInTx(context.Background(), tx, "labels", "events", "bd-1", []string{"c"}, "alice"); err == nil {
			t.Fatal("expected the current-set read error to propagate")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("no write should be attempted after a read failure: %v", err)
		}
	})
}

func TestRemoveLabelInTx(t *testing.T) {
	t.Parallel()

	t.Run("deletes label then records the label_removed event", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM labels WHERE issue_id = ? AND LOWER(label) = ?")).
			WithArgs("bd-1", "bug").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)")).
			WithArgs(sqlmock.AnyArg(), "bd-1", "label_removed", "alice", "Removed label: bug").
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := RemoveLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "bug", "alice"); err != nil {
			t.Fatalf("RemoveLabelInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("no-op delete (label absent) writes no event", func(t *testing.T) {
		t.Parallel()
		// DELETE of a label that was never on the issue affects 0 rows;
		// recording a label_removed event for a removal that never happened
		// pollutes the audit trail (beads-usz1). Expect ONLY the DELETE.
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM labels WHERE issue_id = ? AND LOWER(label) = ?")).
			WithArgs("bd-1", "bug").
			WillReturnResult(sqlmock.NewResult(0, 0))
		if err := RemoveLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "bug", "alice"); err != nil {
			t.Fatalf("RemoveLabelInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("expected no event write on no-op delete: %v", err)
		}
	})

	t.Run("delete error is wrapped (no event write)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("DELETE FROM labels").WillReturnError(errors.New("boom"))
		if err := RemoveLabelInTx(context.Background(), tx, "labels", "events", "bd-1", "bug", "alice"); err == nil {
			t.Fatal("expected wrapped delete error")
		}
	})
}
