package issueops

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCountIsBlockedInconsistenciesInTx(t *testing.T) {
	t.Parallel()

	// Both COUNT queries start with "SELECT COUNT(*) FROM <table> <alias>".
	issuesCount := `SELECT COUNT\(\*\) FROM issues i`
	wispsCount := `SELECT COUNT\(\*\) FROM wisps w`

	t.Run("sums issue + wisp stale counts", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesCount).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(int64(3)))
		mock.ExpectQuery(wispsCount).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(int64(2)))

		got, err := CountIsBlockedInconsistenciesInTx(context.Background(), tx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != 5 {
			t.Fatalf("total = %d, want 5", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("wisps table missing -> returns issue count", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesCount).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(int64(4)))
		mock.ExpectQuery(wispsCount).WillReturnError(tableNotExistErr())

		got, err := CountIsBlockedInconsistenciesInTx(context.Background(), tx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != 4 {
			t.Fatalf("total = %d, want 4 (issues only)", got)
		}
	})

	t.Run("issues count query error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesCount).WillReturnError(errors.New("boom"))
		if _, err := CountIsBlockedInconsistenciesInTx(context.Background(), tx); err == nil {
			t.Fatal("expected issues count error")
		}
	})

	t.Run("wisps count non-missing error propagates", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesCount).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(int64(1)))
		mock.ExpectQuery(wispsCount).WillReturnError(errors.New("wisp boom"))
		if _, err := CountIsBlockedInconsistenciesInTx(context.Background(), tx); err == nil {
			t.Fatal("expected wisps count error")
		}
	})
}
