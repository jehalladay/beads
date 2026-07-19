package issueops

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

func TestIsDependencyTreeEdge(t *testing.T) {
	tests := []struct {
		name    string
		depType types.DependencyType
		want    bool
	}{
		{
			name:    "blocks remains a tree edge",
			depType: types.DepBlocks,
			want:    true,
		},
		{
			name:    "parent-child remains a tree edge",
			depType: types.DepParentChild,
			want:    true,
		},
		{
			name:    "related remains a tree edge",
			depType: types.DepRelated,
			want:    true,
		},
		{
			name:    "relates-to is a graph link, not a tree edge",
			depType: types.DepRelatesTo,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDependencyTreeEdge(tt.depType); got != tt.want {
				t.Fatalf("isDependencyTreeEdge(%q) = %v, want %v", tt.depType, got, tt.want)
			}
		})
	}
}

func TestGetDependencyTreeInTxSkipsRelatesToEdges(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	expectIssue(mock, "root", "Root")
	expectDependencies(mock, "root", []dependencyRow{
		{id: "blocker", depType: string(types.DepBlocks)},
		{id: "related", depType: string(types.DepRelatesTo)},
	})
	expectIssueBatch(mock, []string{"blocker", "related"})
	expectIssue(mock, "blocker", "Blocker")
	expectDependencies(mock, "blocker", nil)
	mock.ExpectRollback()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	tree, err := GetDependencyTreeInTx(context.Background(), tx, "root", 3, false, false)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("GetDependencyTreeInTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}

	if len(tree) != 2 {
		t.Fatalf("len(tree) = %d, want 2 nodes: %+v", len(tree), tree)
	}
	if tree[0].ID != "root" || tree[1].ID != "blocker" {
		t.Fatalf("tree IDs = %v, want [root blocker]", treeIDs(tree))
	}
	if tree[1].EdgeFromParent != types.DepBlocks {
		t.Fatalf("blocker edge = %q, want %q", tree[1].EdgeFromParent, types.DepBlocks)
	}
}

// TestGetDependencyTreeInTxToleratesUnresolvedChild pins beads-s34r: an
// unresolved external/cross-prefix child (allowed by `dep add`, surfaced as a
// placeholder by GetDependenciesWithMetadataInTx per beads-n49j) must NOT abort
// the whole `bd dep tree` render with rc1 "not found". The child is emitted as
// a placeholder leaf node and descent stops there.
func TestGetDependencyTreeInTxToleratesUnresolvedChild(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	expectIssue(mock, "root", "Root")
	expectDependencies(mock, "root", []dependencyRow{
		{id: "OTHER-999", depType: string(types.DepBlocks)},
	})
	// Metadata batch: the child is not a local issue, so it resolves to nothing
	// (GetDependenciesWithMetadataInTx then emits the n49j placeholder).
	expectIssueBatchMissing(mock, "OTHER-999")
	// Recursion re-fetches the child; both tables miss → ErrNotFound. The fix
	// turns this into a placeholder leaf instead of aborting the render.
	expectIssueMissing(mock, "OTHER-999")
	mock.ExpectRollback()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	tree, err := GetDependencyTreeInTx(context.Background(), tx, "root", 3, false, false)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("GetDependencyTreeInTx aborted on an unresolved child (beads-s34r): %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}

	if len(tree) != 2 {
		t.Fatalf("len(tree) = %d, want 2 (root + placeholder child): %v", len(tree), treeIDs(tree))
	}
	if tree[0].ID != "root" || tree[1].ID != "OTHER-999" {
		t.Fatalf("tree IDs = %v, want [root OTHER-999]", treeIDs(tree))
	}
	if tree[1].EdgeFromParent != types.DepBlocks {
		t.Fatalf("child edge = %q, want %q", tree[1].EdgeFromParent, types.DepBlocks)
	}
	if !strings.Contains(tree[1].Title, "unresolved") {
		t.Fatalf("child title = %q, want an unresolved placeholder", tree[1].Title)
	}
}

// TestGetDependencyTreeInTxNotFoundRootStillErrors pins that beads-s34r's
// tolerance is scoped to CHILDREN — a genuinely missing ROOT is still a real
// "no such issue" error, not a silent empty tree.
func TestGetDependencyTreeInTxNotFoundRootStillErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	expectIssueMissing(mock, "nope")
	mock.ExpectRollback()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	_, err = GetDependencyTreeInTx(context.Background(), tx, "nope", 3, false, false)
	_ = tx.Rollback()
	if err == nil {
		t.Fatalf("GetDependencyTreeInTx on a missing ROOT should error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

type dependencyRow struct {
	id      string
	depType string
}

// expectIssueMissing models GetIssueInTx for an id present in neither the
// issues nor the wisps table (both queries return no rows → ErrNotFound).
func expectIssueMissing(mock sqlmock.Sqlmock, id string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + IssueSelectColumns + " FROM issues WHERE id = ?")).
		WithArgs(id).
		WillReturnRows(issueRows())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + IssueSelectColumns + " FROM wisps WHERE id = ?")).
		WithArgs(id).
		WillReturnRows(issueRows())
}

// expectIssueBatchMissing models GetIssuesByIDsInTx for a single id that
// resolves to no local issue (empty wisps table, empty issues result), which is
// how an unresolved cross-prefix dependency target reaches the n49j placeholder.
func expectIssueBatchMissing(mock sqlmock.Sqlmock, id string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+IssueSelectColumns+" FROM issues WHERE id IN (?)")).
		WithArgs(id).
		WillReturnRows(issueRows())
}

func expectIssue(mock sqlmock.Sqlmock, id, title string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + IssueSelectColumns + " FROM issues WHERE id = ?")).
		WithArgs(id).
		WillReturnRows(issueRows().AddRow(issueRowValues(id, title)...))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT label FROM labels WHERE issue_id = ? ORDER BY label")).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"label"}))
}

func expectDependencies(mock sqlmock.Sqlmock, issueID string, deps []dependencyRow) {
	rows := sqlmock.NewRows([]string{"depends_on_id", "type"})
	for _, dep := range deps {
		rows.AddRow(dep.id, dep.depType)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + DepTargetExpr + " AS depends_on_id, type FROM dependencies WHERE issue_id = ?")).
		WithArgs(issueID).
		WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + DepTargetExpr + " AS depends_on_id, type FROM wisp_dependencies WHERE issue_id = ?")).
		WithArgs(issueID).
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_id", "type"}))
}

func expectIssueBatch(mock sqlmock.Sqlmock, ids []string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")).
		WillReturnError(sql.ErrNoRows)

	rows := issueRows()
	for _, id := range ids {
		rows.AddRow(issueRowValues(id, id)...)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+IssueSelectColumns+" FROM issues WHERE id IN (?,?)")).
		WithArgs(ids[0], ids[1]).
		WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT issue_id, label FROM labels WHERE issue_id IN (?,?) ORDER BY issue_id, label")).
		WithArgs(ids[0], ids[1]).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
}

func issueRows() *sqlmock.Rows {
	return sqlmock.NewRows(issueColumns())
}

func issueColumns() []string {
	parts := strings.Split(strings.ReplaceAll(IssueSelectColumns, "\n", " "), ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func issueRowValues(id, title string) []driver.Value {
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
			values = append(values, 1)
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

func treeIDs(tree []*types.TreeNode) []string {
	ids := make([]string, 0, len(tree))
	for _, node := range tree {
		ids = append(ids, node.ID)
	}
	return ids
}
