package issueops

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestIsActiveWispInTx_TransientProbeErrorDisambiguates covers beads-byr7: a
// transient (non-ErrNoRows) error on the wisps probe must NOT be silently
// treated as "not a wisp". IsActiveWispInTx disambiguates against the issues
// table so the bool the 32 write-routing callers receive is correct.
func TestIsActiveWispInTx_TransientProbeErrorDisambiguates(t *testing.T) {
	t.Parallel()

	const wispsProbe = "SELECT 1 FROM wisps WHERE id = ? LIMIT 1"
	const issuesProbe = "SELECT 1 FROM issues WHERE id = ? LIMIT 1"
	probeErr := errors.New("connection reset by peer")

	t.Run("clean wisp hit → true (no issues probe)", func(t *testing.T) {
		t.Parallel()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(wispsProbe)).WithArgs("w-1").
			WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		mock.ExpectCommit()

		tx, _ := db.BeginTx(context.Background(), nil)
		if got := IsActiveWispInTx(context.Background(), tx, "w-1"); !got {
			t.Errorf("clean wisp hit: got false, want true")
		}
		_ = tx.Commit()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("clean not-a-wisp → false (no issues probe)", func(t *testing.T) {
		t.Parallel()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(wispsProbe)).WithArgs("i-1").
			WillReturnError(errNoRows())
		mock.ExpectCommit()

		tx, _ := db.BeginTx(context.Background(), nil)
		if got := IsActiveWispInTx(context.Background(), tx, "i-1"); got {
			t.Errorf("clean not-a-wisp: got true, want false")
		}
		_ = tx.Commit()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("transient wisp error + row IS in issues → false", func(t *testing.T) {
		t.Parallel()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(wispsProbe)).WithArgs("i-2").
			WillReturnError(probeErr)
		mock.ExpectQuery(regexp.QuoteMeta(issuesProbe)).WithArgs("i-2").
			WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		mock.ExpectCommit()

		tx, _ := db.BeginTx(context.Background(), nil)
		if got := IsActiveWispInTx(context.Background(), tx, "i-2"); got {
			t.Errorf("transient wisp err + in issues: got true, want false (it's a permanent issue)")
		}
		_ = tx.Commit()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("transient wisp error + row NOT in issues → true (the byr7 fix)", func(t *testing.T) {
		t.Parallel()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(wispsProbe)).WithArgs("w-2").
			WillReturnError(probeErr)
		mock.ExpectQuery(regexp.QuoteMeta(issuesProbe)).WithArgs("w-2").
			WillReturnError(errNoRows())
		mock.ExpectCommit()

		tx, _ := db.BeginTx(context.Background(), nil)
		// Pre-byr7 this returned false → misroute a real wisp to the issues
		// table = silent lost update. The fix routes it as a wisp.
		if got := IsActiveWispInTx(context.Background(), tx, "w-2"); !got {
			t.Errorf("transient wisp err + not in issues: got false, want true (route as wisp, not misroute to issues)")
		}
		_ = tx.Commit()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("both probes error → false (historical fallback, no worse than pre-byr7)", func(t *testing.T) {
		t.Parallel()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(wispsProbe)).WithArgs("x-1").
			WillReturnError(probeErr)
		mock.ExpectQuery(regexp.QuoteMeta(issuesProbe)).WithArgs("x-1").
			WillReturnError(probeErr)
		mock.ExpectCommit()

		tx, _ := db.BeginTx(context.Background(), nil)
		if got := IsActiveWispInTx(context.Background(), tx, "x-1"); got {
			t.Errorf("both probes error: got true, want false (fallback)")
		}
		_ = tx.Commit()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

// errNoRows returns the driver's ErrNoRows so the switch's errors.Is check
// (which the fix depends on) is exercised exactly as production hits it.
// database/sql surfaces sql.ErrNoRows from Row.Scan when no row matched;
// sqlmock's WillReturnError with sql.ErrNoRows reproduces that path.
func errNoRows() error {
	return sql.ErrNoRows
}
