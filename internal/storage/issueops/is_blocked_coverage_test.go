package issueops

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// TestOptionalBlockedTable asserts only the wisp tables are treated as
// optional (their absence is tolerated); the durable tables are required.
func TestOptionalBlockedTable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		table string
		want  bool
	}{
		{"wisps", true},
		{"wisp_dependencies", true},
		{"issues", false},
		{"dependencies", false},
	} {
		if got := optionalBlockedTable(tc.table); got != tc.want {
			t.Errorf("optionalBlockedTable(%q) = %v, want %v", tc.table, got, tc.want)
		}
	}
}

// TestScanIssueCountsInTx covers the status-count aggregate scan: the happy
// path populates every count field, and a query error is wrapped.
func TestScanIssueCountsInTx(t *testing.T) {
	t.Parallel()

	q := regexp.QuoteMeta("COUNT(*) AS total")

	t.Run("populates all count fields", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(q).WillReturnRows(
			sqlmock.NewRows([]string{"total", "open", "in_progress", "closed", "deferred", "pinned"}).
				AddRow(10, 4, 2, 3, 1, 5))
		var stats types.Statistics
		if err := ScanIssueCountsInTx(context.Background(), tx, &stats); err != nil {
			t.Fatalf("ScanIssueCountsInTx: %v", err)
		}
		if stats.TotalIssues != 10 || stats.OpenIssues != 4 || stats.InProgressIssues != 2 ||
			stats.ClosedIssues != 3 || stats.DeferredIssues != 1 || stats.PinnedIssues != 5 {
			t.Fatalf("stats = %+v, want total=10 open=4 inprog=2 closed=3 deferred=1 pinned=5", stats)
		}
	})

	t.Run("wraps scan error", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		err := ScanIssueCountsInTx(context.Background(), tx, &types.Statistics{})
		if err == nil || err.Error() != "scan issue counts: boom" {
			t.Fatalf("err = %v, want 'scan issue counts: boom'", err)
		}
	})
}

// TestIsBlockedInTx covers the is-blocked resolver branches: not-blocked
// short-circuits before touching dep tables; a blocked row with no live edges
// returns blocked-but-no-blockers; a blocked row filters out closed/pinned
// blockers and annotates non-"blocks" edge types; and a hard read error on the
// required issues table propagates.
func TestIsBlockedInTx(t *testing.T) {
	t.Parallel()

	flagQ := regexp.QuoteMeta("SELECT is_blocked FROM issues WHERE id = ?")
	edgeQ := `SELECT .* AS depends_on_id, type FROM dependencies`
	wispEdgeQ := `FROM wisp_dependencies`
	statusQ := `SELECT id, status FROM issues WHERE id IN`

	t.Run("not blocked short-circuits", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(flagQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"is_blocked"}).AddRow(0))
		blocked, blockers, err := IsBlockedInTx(context.Background(), tx, "bd-1")
		if err != nil {
			t.Fatalf("IsBlockedInTx: %v", err)
		}
		if blocked || blockers != nil {
			t.Fatalf("got (%v, %v), want (false, nil)", blocked, blockers)
		}
	})

	t.Run("blocked with no live edges returns no blockers", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(flagQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"is_blocked"}).AddRow(1))
		mock.ExpectQuery(edgeQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"depends_on_id", "type"}))
		mock.ExpectQuery(wispEdgeQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"depends_on_id", "type"}))
		blocked, blockers, err := IsBlockedInTx(context.Background(), tx, "bd-1")
		if err != nil {
			t.Fatalf("IsBlockedInTx: %v", err)
		}
		if !blocked || blockers != nil {
			t.Fatalf("got (%v, %v), want (true, nil)", blocked, blockers)
		}
	})

	t.Run("filters closed blockers and annotates edge type", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(flagQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"is_blocked"}).AddRow(1))
		mock.ExpectQuery(edgeQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"depends_on_id", "type"}).
				AddRow("bd-open", "blocks").
				AddRow("bd-wait", "waits-for").
				AddRow("bd-done", "blocks"))
		mock.ExpectQuery(wispEdgeQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"depends_on_id", "type"}))
		mock.ExpectQuery(statusQ).WillReturnRows(
			sqlmock.NewRows([]string{"id", "status"}).
				AddRow("bd-open", "open").
				AddRow("bd-wait", "open").
				AddRow("bd-done", "closed"))
		// wisps status lookup (second table in loadStatusByIDInTx).
		mock.ExpectQuery(`SELECT id, status FROM wisps WHERE id IN`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
		blocked, blockers, err := IsBlockedInTx(context.Background(), tx, "bd-1")
		if err != nil {
			t.Fatalf("IsBlockedInTx: %v", err)
		}
		if !blocked {
			t.Fatal("want blocked=true")
		}
		// bd-done is closed → filtered; bd-open kept bare; bd-wait annotated.
		want := map[string]bool{"bd-open": true, "bd-wait (waits-for)": true}
		if len(blockers) != 2 {
			t.Fatalf("blockers = %v, want 2 entries", blockers)
		}
		for _, b := range blockers {
			if !want[b] {
				t.Errorf("unexpected blocker %q (blockers=%v)", b, blockers)
			}
		}
	})

	t.Run("hard read error on issues table propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(flagQ).WithArgs("bd-1").WillReturnError(errors.New("boom"))
		if _, _, err := IsBlockedInTx(context.Background(), tx, "bd-1"); err == nil {
			t.Fatal("expected error on issues-table failure, got nil")
		}
	})
}
