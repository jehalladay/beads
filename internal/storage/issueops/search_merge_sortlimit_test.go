package issueops

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// issueRowValuesPri builds a canonical issue row with a chosen id/title/priority
// so a merged issues+wisps set can be asserted for global sort order. Mirrors
// issueRowValues (dependency_tree_test.go) but lets the test vary priority.
func issueRowValuesPri(id, title string, priority int) []driver.Value {
	values := make([]driver.Value, 0, len(issueColumns()))
	for _, col := range issueColumns() {
		switch col {
		case "id":
			values = append(values, id)
		case "title":
			values = append(values, title)
		case "description", "design", "acceptance_criteria", "notes":
			values = append(values, "")
		case "status":
			values = append(values, string(types.StatusOpen))
		case "priority":
			values = append(values, priority)
		case "issue_type":
			values = append(values, string(types.TypeTask))
		case "compaction_level":
			values = append(values, 0)
		default:
			values = append(values, nil)
		}
	}
	return values
}

// TestSearchIssuesInTx_MergeRespectsLimit covers beads-4t1m: when both the
// issues and wisps tables return rows, the merged result must be truncated to
// filter.Limit. Before the fix, each half was LIMIT'd independently and the two
// were concatenated, so a Limit=N search could return up to 2N rows.
func TestSearchIssuesInTx_MergeRespectsLimit(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	// Pattern B (Limit>0): id scan on issues returns 2 ids (its own LIMIT).
	mock.ExpectQuery(`SELECT .*id FROM issues.*LIMIT 3`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-1").AddRow("bd-2"))
	mock.ExpectQuery(`SELECT .* FROM issues WHERE id IN`).
		WillReturnRows(sqlmock.NewRows(issueColumns()).
			AddRow(issueRowValuesPri("bd-1", "i1", 1)...).
			AddRow(issueRowValuesPri("bd-2", "i2", 2)...))
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
	// wisps non-empty probe.
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	// wisps id scan returns 2 more ids (its own independent LIMIT 3).
	mock.ExpectQuery(`SELECT .*id FROM wisps.*LIMIT 3`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-w1").AddRow("bd-w2"))
	mock.ExpectQuery(`SELECT .* FROM wisps WHERE id IN`).
		WillReturnRows(sqlmock.NewRows(issueColumns()).
			AddRow(issueRowValuesPri("bd-w1", "w1", 0)...).
			AddRow(issueRowValuesPri("bd-w2", "w2", 3)...))
	mock.ExpectQuery(`SELECT issue_id, label FROM wisp_labels WHERE issue_id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))

	got, err := SearchIssuesInTx(context.Background(), tx, "", types.IssueFilter{Limit: 3, SortBy: "priority"})
	if err != nil {
		t.Fatalf("SearchIssuesInTx: %v", err)
	}

	// 2 issues + 2 wisps = 4 candidates, but Limit=3 must truncate to 3.
	if len(got) != 3 {
		ids := make([]string, len(got))
		for i, g := range got {
			ids[i] = g.ID
		}
		t.Fatalf("got %d issues %v, want 3 (Limit not enforced across merge)", len(got), ids)
	}

	// Global sort by priority ASC (default dir) must interleave both halves:
	// bd-w1(P0), bd-1(P1), bd-2(P2) — the P3 wisp bd-w2 is dropped by the limit.
	// Before the fix the order was [issues]++[wisps] = bd-1,bd-2,bd-w1 (wrong).
	wantOrder := []string{"bd-w1", "bd-1", "bd-2"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			ids := make([]string, len(got))
			for j, g := range got {
				ids[j] = g.ID
			}
			t.Fatalf("merged order = %v, want %v (global priority sort across the merge)", ids, wantOrder)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
