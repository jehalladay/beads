package issueops

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestGetDependencyCountsInTx covers the blocker/dependent count fan-out: the
// empty-input short-circuit, the wisps-present path (both dep tables queried),
// the wisps-empty path (issues table only), and the probe-error path.
func TestGetDependencyCountsInTx(t *testing.T) {
	t.Parallel()

	probeQ := regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")
	// blocker count: WHERE issue_id IN (...) AND type = 'blocks'
	depBlockers := `SELECT issue_id, COUNT\(\*\) as cnt\s+FROM dependencies\s+WHERE issue_id IN`
	// dependent count: WHERE <target-expr> IN (...) AND type = 'blocks'
	depDependents := `SELECT .* AS depends_on_id, COUNT\(\*\) as cnt\s+FROM dependencies`
	wispBlockers := `FROM wisp_dependencies\s+WHERE issue_id IN`

	t.Run("empty input short-circuits", func(t *testing.T) {
		_, _, tx := beginMockTx(t)
		got, err := GetDependencyCountsInTx(context.Background(), tx, nil)
		if err != nil {
			t.Fatalf("GetDependencyCountsInTx: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want empty map", got)
		}
	})

	t.Run("wisps empty queries issues table only", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		// Probe returns no rows -> Scan yields sql.ErrNoRows -> wisps treated
		// empty -> only "dependencies" is queried.
		mock.ExpectQuery(probeQ).WillReturnRows(sqlmock.NewRows([]string{"1"}))
		mock.ExpectQuery(depBlockers).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "cnt"}).AddRow("bd-1", 2))
		mock.ExpectQuery(depDependents).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"depends_on_id", "cnt"}).AddRow("bd-1", 1))
		got, err := GetDependencyCountsInTx(context.Background(), tx, []string{"bd-1"})
		if err != nil {
			t.Fatalf("GetDependencyCountsInTx: %v", err)
		}
		if got["bd-1"].DependencyCount != 2 || got["bd-1"].DependentCount != 1 {
			t.Fatalf("counts = %+v, want dep=2 dependent=1", got["bd-1"])
		}
	})

	t.Run("wisps present queries both tables and sums", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		// Probe returns a row -> both dep tables queried.
		mock.ExpectQuery(probeQ).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		// dependencies: blockers + dependents
		mock.ExpectQuery(depBlockers).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "cnt"}).AddRow("bd-1", 2))
		mock.ExpectQuery(depDependents).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"depends_on_id", "cnt"}).AddRow("bd-1", 1))
		// wisp_dependencies: blockers + dependents
		mock.ExpectQuery(wispBlockers).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "cnt"}).AddRow("bd-1", 3))
		mock.ExpectQuery(`FROM wisp_dependencies`).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"depends_on_id", "cnt"}).AddRow("bd-1", 4))
		got, err := GetDependencyCountsInTx(context.Background(), tx, []string{"bd-1"})
		if err != nil {
			t.Fatalf("GetDependencyCountsInTx: %v", err)
		}
		if got["bd-1"].DependencyCount != 5 || got["bd-1"].DependentCount != 5 {
			t.Fatalf("counts = %+v, want dep=5 (2+3) dependent=5 (1+4)", got["bd-1"])
		}
	})

	t.Run("probe error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probeQ).WillReturnError(errors.New("boom"))
		if _, err := GetDependencyCountsInTx(context.Background(), tx, []string{"bd-1"}); err == nil {
			t.Fatal("expected probe error, got nil")
		}
	})

	t.Run("blocker query error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probeQ).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		mock.ExpectQuery(depBlockers).WithArgs("bd-1").WillReturnError(errors.New("boom"))
		if _, err := GetDependencyCountsInTx(context.Background(), tx, []string{"bd-1"}); err == nil {
			t.Fatal("expected blocker query error, got nil")
		}
	})

	t.Run("dependent query error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probeQ).WillReturnRows(sqlmock.NewRows([]string{"1"}))
		mock.ExpectQuery(depBlockers).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "cnt"}).AddRow("bd-1", 1))
		mock.ExpectQuery(depDependents).WithArgs("bd-1").WillReturnError(errors.New("boom"))
		if _, err := GetDependencyCountsInTx(context.Background(), tx, []string{"bd-1"}); err == nil {
			t.Fatal("expected dependent query error, got nil")
		}
	})

	t.Run("blocker scan error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probeQ).WillReturnRows(sqlmock.NewRows([]string{"1"}))
		// Return a single-column row so Scan(&id, &cnt) fails.
		mock.ExpectQuery(depBlockers).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id"}).AddRow("bd-1"))
		if _, err := GetDependencyCountsInTx(context.Background(), tx, []string{"bd-1"}); err == nil {
			t.Fatal("expected blocker scan error, got nil")
		}
	})

	t.Run("optional wisp table not-existing is skipped", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		// Probe reports wisps present so wisp_dependencies is queried, but that
		// table is missing (error 1146) -> the optional table is skipped rather
		// than propagating an error.
		mock.ExpectQuery(probeQ).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		mock.ExpectQuery(depBlockers).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "cnt"}).AddRow("bd-1", 2))
		mock.ExpectQuery(depDependents).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"depends_on_id", "cnt"}).AddRow("bd-1", 1))
		mock.ExpectQuery(wispBlockers).WithArgs("bd-1").WillReturnError(
			errors.New("Error 1146: Table 'beads.wisp_dependencies' doesn't exist"))
		got, err := GetDependencyCountsInTx(context.Background(), tx, []string{"bd-1"})
		if err != nil {
			t.Fatalf("GetDependencyCountsInTx: %v", err)
		}
		if got["bd-1"].DependencyCount != 2 || got["bd-1"].DependentCount != 1 {
			t.Fatalf("counts = %+v, want dep=2 dependent=1 (wisp table skipped)", got["bd-1"])
		}
	})
}
