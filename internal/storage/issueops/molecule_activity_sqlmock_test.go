package issueops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// expectWisp mocks IsActiveWispInTx's probe so routing selects the wisp tables:
// SELECT 1 FROM wisps WHERE id = ? LIMIT 1 -> a row.
func expectWisp(mock sqlmock.Sqlmock, id string) {
	mock.ExpectQuery(`SELECT 1 FROM wisps WHERE id = \? LIMIT 1`).
		WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
}

func TestGetMoleculeLastActivityInTx(t *testing.T) {
	t.Parallel()

	const (
		childQ   = `SELECT issue_id FROM \S+\s+WHERE \S+ = \? AND type = 'parent-child'`
		updOneQ  = `SELECT updated_at FROM \S+ WHERE id = \?`
		batchUpd = `SELECT id, updated_at FROM \S+ WHERE id IN \(\S+\) ORDER BY updated_at DESC LIMIT 1`
		batchCls = `SELECT id, closed_at FROM \S+ WHERE id IN \(\S+\) AND closed_at IS NOT NULL ORDER BY closed_at DESC LIMIT 1`
	)

	t.Run("no children -> molecule_updated", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "mol-1")
		mock.ExpectQuery(childQ).WithArgs("mol-1").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id"}))
		ts := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		mock.ExpectQuery(updOneQ).WithArgs("mol-1").
			WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(ts))

		got, err := GetMoleculeLastActivityInTx(context.Background(), tx, "mol-1")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got.MoleculeID != "mol-1" || got.Source != "molecule_updated" || !got.LastActivity.Equal(ts) {
			t.Fatalf("got %+v, want molecule_updated at %v", got, ts)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("no children + molecule not found -> error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "gone")
		mock.ExpectQuery(childQ).WithArgs("gone").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id"}))
		mock.ExpectQuery(updOneQ).WithArgs("gone").WillReturnError(errors.New("no such row"))

		if _, err := GetMoleculeLastActivityInTx(context.Background(), tx, "gone"); err == nil {
			t.Fatal("expected not-found error")
		}
	})

	t.Run("children -> step_updated (closed older / absent)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "mol-2")
		mock.ExpectQuery(childQ).WithArgs("mol-2").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id"}).AddRow("c1").AddRow("c2"))
		upd := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
		mock.ExpectQuery(batchUpd).WithArgs("c1", "c2").
			WillReturnRows(sqlmock.NewRows([]string{"id", "updated_at"}).AddRow("c2", upd))
		// closed query returns no rows -> stays step_updated
		mock.ExpectQuery(batchCls).WithArgs("c1", "c2").
			WillReturnRows(sqlmock.NewRows([]string{"id", "closed_at"}))

		got, err := GetMoleculeLastActivityInTx(context.Background(), tx, "mol-2")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got.Source != "step_updated" || got.SourceStepID != "c2" || !got.LastActivity.Equal(upd) {
			t.Fatalf("got %+v, want step_updated c2 at %v", got, upd)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("children -> step_closed (closed after updated)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "mol-3")
		mock.ExpectQuery(childQ).WithArgs("mol-3").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id"}).AddRow("c1"))
		upd := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
		mock.ExpectQuery(batchUpd).WithArgs("c1").
			WillReturnRows(sqlmock.NewRows([]string{"id", "updated_at"}).AddRow("c1", upd))
		closed := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
		mock.ExpectQuery(batchCls).WithArgs("c1").
			WillReturnRows(sqlmock.NewRows([]string{"id", "closed_at"}).AddRow("c1", closed))

		got, err := GetMoleculeLastActivityInTx(context.Background(), tx, "mol-3")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got.Source != "step_closed" || got.SourceStepID != "c1" || !got.LastActivity.Equal(closed) {
			t.Fatalf("got %+v, want step_closed c1 at %v", got, closed)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("children query error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "mol-e")
		mock.ExpectQuery(childQ).WithArgs("mol-e").WillReturnError(errors.New("boom"))
		if _, err := GetMoleculeLastActivityInTx(context.Background(), tx, "mol-e"); err == nil {
			t.Fatal("expected children query error")
		}
	})

	t.Run("child scan error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "mol-s")
		// A row with a non-string value forces Scan into a string to error.
		rows := sqlmock.NewRows([]string{"issue_id"}).
			AddRow("c1").RowError(0, errors.New("row broke"))
		mock.ExpectQuery(childQ).WithArgs("mol-s").WillReturnRows(rows)
		if _, err := GetMoleculeLastActivityInTx(context.Background(), tx, "mol-s"); err == nil {
			t.Fatal("expected child iteration error")
		}
	})

	t.Run("children but no updated row -> no children found error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "mol-n")
		mock.ExpectQuery(childQ).WithArgs("mol-n").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id"}).AddRow("c1"))
		// batch updated errors -> lastUpdatedID stays ""
		mock.ExpectQuery(batchUpd).WithArgs("c1").WillReturnError(errors.New("no data"))
		mock.ExpectQuery(batchCls).WithArgs("c1").
			WillReturnRows(sqlmock.NewRows([]string{"id", "closed_at"}))
		if _, err := GetMoleculeLastActivityInTx(context.Background(), tx, "mol-n"); err == nil {
			t.Fatal("expected no-children-found error")
		}
	})

	t.Run("wisp routing path", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectWisp(mock, "wisp-1")
		// child query must hit wisp_dependencies via depends_on_wisp_id
		mock.ExpectQuery(`SELECT issue_id FROM wisp_dependencies\s+WHERE depends_on_wisp_id = \? AND type = 'parent-child'`).
			WithArgs("wisp-1").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id"}))
		ts := time.Date(2026, 7, 2, 8, 0, 0, 0, time.UTC)
		mock.ExpectQuery(`SELECT updated_at FROM wisps WHERE id = \?`).WithArgs("wisp-1").
			WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(ts))

		got, err := GetMoleculeLastActivityInTx(context.Background(), tx, "wisp-1")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got.Source != "molecule_updated" || !got.LastActivity.Equal(ts) {
			t.Fatalf("got %+v, want molecule_updated at %v", got, ts)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})
}
