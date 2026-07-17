package issueops

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// TestShouldBeBlockedDisjunction asserts the disjunction template interpolates
// the row alias, dependency table, and the shared waits-for gate SQL into every
// EXISTS leg — the five reasons a row should carry is_blocked=1.
func TestShouldBeBlockedDisjunction(t *testing.T) {
	t.Parallel()

	got := shouldBeBlockedDisjunction("i", "dependencies")

	// Every leg keys off the row alias and joins through the dep table.
	for _, want := range []string{
		"FROM dependencies d",
		"WHERE d.issue_id = i.id",
		"JOIN issues t ON t.id = d.depends_on_issue_id",
		"JOIN wisps t ON t.id = d.depends_on_wisp_id",
		"d.type = 'parent-child'",
		"d.type = 'waits-for'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("disjunction missing %q\nfull:\n%s", want, got)
		}
	}

	// The waits-for leg must embed the shared gate template, not re-invent it.
	if !strings.Contains(got, waitsForGateBlockedSQL) {
		t.Errorf("disjunction did not embed waitsForGateBlockedSQL")
	}

	// Exactly five top-level legs — two hard-blocker (issue/wisp), two
	// parent-child (issue/wisp), one waits-for — each keyed off the row's own
	// id via "d.issue_id = <alias>.id". (The embedded waits-for gate adds more
	// EXISTS clauses, but those key off cd.issue_id, not the row alias.)
	if n := strings.Count(got, "WHERE d.issue_id = i.id"); n != 5 {
		t.Errorf("disjunction has %d top-level legs, want 5", n)
	}

	// The wisps alias variant substitutes the wisp dep table / target column.
	wispGot := shouldBeBlockedDisjunction("w", "wisp_dependencies")
	if !strings.Contains(wispGot, "FROM wisp_dependencies d") || !strings.Contains(wispGot, "WHERE d.issue_id = w.id") {
		t.Errorf("wisp variant did not substitute alias/dep table:\n%s", wispGot)
	}
}

// TestCountStaleIsBlockedSQL asserts the mark/unmark stale-count query wraps the
// disjunction in both an is_blocked=0-should-be-blocked and an
// is_blocked=1-should-not branch over the target table/alias.
func TestCountStaleIsBlockedSQL(t *testing.T) {
	t.Parallel()

	got := countStaleIsBlockedSQL("issues", "i", "dependencies")

	for _, want := range []string{
		"SELECT COUNT(*) FROM issues i",
		"i.is_blocked = 0",
		"i.status <> 'closed' AND i.status <> 'pinned'",
		"i.is_blocked = 1",
		"i.status = 'closed' OR i.status = 'pinned'",
		"NOT (",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("count-stale SQL missing %q\nfull:\n%s", want, got)
		}
	}

	// The disjunction appears twice: once in the mark branch, once (negated) in
	// the unmark branch.
	disj := shouldBeBlockedDisjunction("i", "dependencies")
	if n := strings.Count(normalizeWS(got), normalizeWS(disj)); n != 2 {
		t.Errorf("disjunction appears %d times in count-stale SQL, want 2", n)
	}
}

func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestCountRows exercises the scalar COUNT(*) helper: a returned row, an empty
// result, a query error, and a scan error.
func TestCountRows(t *testing.T) {
	t.Parallel()

	t.Run("returns the scalar", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(
			sqlmock.NewRows([]string{"n"}).AddRow(int64(7)))
		n, err := countRows(context.Background(), tx, "SELECT COUNT(*) FROM issues")
		if err != nil {
			t.Fatalf("countRows: %v", err)
		}
		if n != 7 {
			t.Fatalf("countRows = %d, want 7", n)
		}
	})

	t.Run("empty result is zero", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"n"}))
		n, err := countRows(context.Background(), tx, "SELECT COUNT(*) FROM issues")
		if err != nil {
			t.Fatalf("countRows: %v", err)
		}
		if n != 0 {
			t.Fatalf("countRows = %d, want 0", n)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("boom"))
		if _, err := countRows(context.Background(), tx, "SELECT COUNT(*) FROM issues"); err == nil {
			t.Fatal("countRows: expected query error, got nil")
		}
	})

	t.Run("scan error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(
			sqlmock.NewRows([]string{"n"}).AddRow("not-an-int"))
		if _, err := countRows(context.Background(), tx, "SELECT COUNT(*) FROM issues"); err == nil {
			t.Fatal("countRows: expected scan error, got nil")
		}
	})
}

// TestAllIDs exercises the "SELECT id FROM <table>" lister: rows, empty, query
// error, and scan error.
func TestAllIDs(t *testing.T) {
	t.Parallel()

	t.Run("lists ids", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT id FROM issues").WillReturnRows(
			sqlmock.NewRows([]string{"id"}).AddRow("bd-1").AddRow("bd-2"))
		ids, err := allIDs(context.Background(), tx, "issues")
		if err != nil {
			t.Fatalf("allIDs: %v", err)
		}
		if len(ids) != 2 || ids[0] != "bd-1" || ids[1] != "bd-2" {
			t.Fatalf("allIDs = %v, want [bd-1 bd-2]", ids)
		}
	})

	t.Run("empty table", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT id FROM wisps").WillReturnRows(sqlmock.NewRows([]string{"id"}))
		ids, err := allIDs(context.Background(), tx, "wisps")
		if err != nil {
			t.Fatalf("allIDs: %v", err)
		}
		if len(ids) != 0 {
			t.Fatalf("allIDs = %v, want empty", ids)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT id FROM issues").WillReturnError(errors.New("boom"))
		if _, err := allIDs(context.Background(), tx, "issues"); err == nil {
			t.Fatal("allIDs: expected query error, got nil")
		}
	})

	t.Run("scan error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT id FROM issues").WillReturnRows(
			sqlmock.NewRows([]string{"id"}).AddRow(nil))
		if _, err := allIDs(context.Background(), tx, "issues"); err == nil {
			t.Fatal("allIDs: expected scan error on NULL id, got nil")
		}
	})
}

// TestChangedIssueIDs covers the dolt_diff issue-id reader: it keeps valid ids
// (COALESCE(to_id, from_id)) and drops NULL sentinels.
func TestChangedIssueIDs(t *testing.T) {
	t.Parallel()

	fromCommit := "abc123"
	diffRe := regexp.QuoteMeta("dolt_diff('" + fromCommit + "', 'WORKING', 'issues')")

	t.Run("keeps valid ids and drops nulls", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(diffRe).WillReturnRows(
			sqlmock.NewRows([]string{"id"}).AddRow("bd-1").AddRow(nil).AddRow("bd-2"))
		ids, err := changedIssueIDs(context.Background(), tx, fromCommit)
		if err != nil {
			t.Fatalf("changedIssueIDs: %v", err)
		}
		if len(ids) != 2 || ids[0] != "bd-1" || ids[1] != "bd-2" {
			t.Fatalf("changedIssueIDs = %v, want [bd-1 bd-2]", ids)
		}
	})

	t.Run("query error propagates (reshaped table)", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(diffRe).WillReturnError(errors.New("schema changed"))
		if _, err := changedIssueIDs(context.Background(), tx, fromCommit); err == nil {
			t.Fatal("changedIssueIDs: expected query error, got nil")
		}
	})
}

// TestChangedDependencyEdges covers the dolt_diff dependencies reader: it emits
// one edge per non-null side (from and to) and skips a side whose issue id is
// NULL.
func TestChangedDependencyEdges(t *testing.T) {
	t.Parallel()

	fromCommit := "def456"
	diffRe := regexp.QuoteMeta("dolt_diff('" + fromCommit + "', 'WORKING', 'dependencies')")
	cols := []string{"from_issue_id", "from_target", "from_type", "to_issue_id", "to_target", "to_type"}

	t.Run("emits both sides when present", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(diffRe).WillReturnRows(
			sqlmock.NewRows(cols).AddRow("bd-a", "bd-x", "blocks", "bd-b", "bd-y", "parent-child"))
		edges, err := changedDependencyEdges(context.Background(), tx, fromCommit)
		if err != nil {
			t.Fatalf("changedDependencyEdges: %v", err)
		}
		if len(edges) != 2 {
			t.Fatalf("got %d edges, want 2: %+v", len(edges), edges)
		}
		if edges[0] != (changedDepEdge{"bd-a", "bd-x", types.DependencyType("blocks")}) {
			t.Errorf("from-side edge = %+v", edges[0])
		}
		if edges[1] != (changedDepEdge{"bd-b", "bd-y", types.DependencyType("parent-child")}) {
			t.Errorf("to-side edge = %+v", edges[1])
		}
	})

	t.Run("skips a null issue-id side", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		// Added edge: from-side is all NULL (row did not exist pre-merge).
		mock.ExpectQuery(diffRe).WillReturnRows(
			sqlmock.NewRows(cols).AddRow(nil, nil, nil, "bd-b", "bd-y", "blocks"))
		edges, err := changedDependencyEdges(context.Background(), tx, fromCommit)
		if err != nil {
			t.Fatalf("changedDependencyEdges: %v", err)
		}
		if len(edges) != 1 || edges[0].issueID != "bd-b" {
			t.Fatalf("got %+v, want single to-side edge for bd-b", edges)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(diffRe).WillReturnError(errors.New("boom"))
		if _, err := changedDependencyEdges(context.Background(), tx, fromCommit); err == nil {
			t.Fatal("changedDependencyEdges: expected query error, got nil")
		}
	})
}

// tableMissingErr simulates a MySQL/Dolt "table doesn't exist" (1146) so the
// wisp-table-optional fallback paths can be exercised without a real DB.
var tableMissingErr = errors.New("Error 1146: Table 'wisp_dependencies' doesn't exist")

// TestGetChildrenWithParentsInTx covers the parent-child map builder over both
// dep tables, the empty-input short-circuit, the optional wisp-table fallback,
// and a hard query error on the required table.
func TestGetChildrenWithParentsInTx(t *testing.T) {
	t.Parallel()

	childQ := `SELECT issue_id, .* FROM dependencies\s+WHERE type = 'parent-child'`
	wispChildQ := `SELECT issue_id, .* FROM wisp_dependencies\s+WHERE type = 'parent-child'`

	t.Run("empty input short-circuits", func(t *testing.T) {
		_, _, tx := beginMockTx(t)
		got, err := GetChildrenWithParentsInTx(context.Background(), tx, nil)
		if err != nil || got != nil {
			t.Fatalf("empty input: got %v, %v; want nil, nil", got, err)
		}
	})

	t.Run("maps children to parents across both tables", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(childQ).WithArgs("bd-parent").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).AddRow("bd-child1", "bd-parent"))
		mock.ExpectQuery(wispChildQ).WithArgs("bd-parent").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).AddRow("bd-wchild", "bd-parent"))
		got, err := GetChildrenWithParentsInTx(context.Background(), tx, []string{"bd-parent"})
		if err != nil {
			t.Fatalf("GetChildrenWithParentsInTx: %v", err)
		}
		if got["bd-child1"] != "bd-parent" || got["bd-wchild"] != "bd-parent" {
			t.Fatalf("map = %v, want both children -> bd-parent", got)
		}
	})

	t.Run("missing wisp table is tolerated", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(childQ).WithArgs("bd-p").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).AddRow("bd-c", "bd-p"))
		mock.ExpectQuery(wispChildQ).WithArgs("bd-p").WillReturnError(tableMissingErr)
		got, err := GetChildrenWithParentsInTx(context.Background(), tx, []string{"bd-p"})
		if err != nil {
			t.Fatalf("GetChildrenWithParentsInTx: %v", err)
		}
		if len(got) != 1 || got["bd-c"] != "bd-p" {
			t.Fatalf("map = %v, want single bd-c -> bd-p", got)
		}
	})

	t.Run("hard error on required table propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(childQ).WithArgs("bd-p").WillReturnError(errors.New("boom"))
		if _, err := GetChildrenWithParentsInTx(context.Background(), tx, []string{"bd-p"}); err == nil {
			t.Fatal("expected error on required-table failure, got nil")
		}
	})
}

// TestGetChildrenOfIssuesInTx covers the child-id lister: the empty-input
// short-circuit and the happy path over both dep tables.
func TestGetChildrenOfIssuesInTx(t *testing.T) {
	t.Parallel()

	t.Run("empty input short-circuits", func(t *testing.T) {
		_, _, tx := beginMockTx(t)
		got, err := GetChildrenOfIssuesInTx(context.Background(), tx, nil)
		if err != nil || got != nil {
			t.Fatalf("empty input: got %v, %v; want nil, nil", got, err)
		}
	})

	t.Run("collects children across both tables", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`SELECT issue_id FROM dependencies`).WithArgs("bd-p").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id"}).AddRow("bd-c1"))
		mock.ExpectQuery(`SELECT issue_id FROM wisp_dependencies`).WithArgs("bd-p").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id"}).AddRow("bd-c2"))
		got, err := GetChildrenOfIssuesInTx(context.Background(), tx, []string{"bd-p"})
		if err != nil {
			t.Fatalf("GetChildrenOfIssuesInTx: %v", err)
		}
		if len(got) != 2 || got[0] != "bd-c1" || got[1] != "bd-c2" {
			t.Fatalf("children = %v, want [bd-c1 bd-c2]", got)
		}
	})
}

// TestLoadBlockingDepsForIssueIDsInTx covers the blocking-dep loader: it selects
// only blocks/waits-for/conditional-blocks edges and scans metadata.
func TestLoadBlockingDepsForIssueIDsInTx(t *testing.T) {
	t.Parallel()

	q := `SELECT issue_id, .* AS depends_on_id, type, metadata FROM dependencies`

	t.Run("scans blocking deps", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(q).WithArgs("bd-1").WillReturnRows(
			sqlmock.NewRows([]string{"issue_id", "depends_on_id", "type", "metadata"}).
				AddRow("bd-1", "bd-dep", "blocks", nil))
		deps, err := loadBlockingDepsForIssueIDsInTx(context.Background(), tx, []string{"dependencies"}, []string{"bd-1"})
		if err != nil {
			t.Fatalf("loadBlockingDepsForIssueIDsInTx: %v", err)
		}
		if len(deps) != 1 || deps[0].issueID != "bd-1" || deps[0].dependsOnID != "bd-dep" || deps[0].depType != "blocks" {
			t.Fatalf("deps = %+v, want one bd-1->bd-dep blocks", deps)
		}
	})

	t.Run("missing optional table breaks cleanly", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM wisp_dependencies`).WithArgs("bd-1").WillReturnError(tableMissingErr)
		deps, err := loadBlockingDepsForIssueIDsInTx(context.Background(), tx, []string{"wisp_dependencies"}, []string{"bd-1"})
		if err != nil {
			t.Fatalf("loadBlockingDepsForIssueIDsInTx: %v", err)
		}
		if len(deps) != 0 {
			t.Fatalf("deps = %+v, want none", deps)
		}
	})
}

// TestLoadParentIDsForChildrenInTx covers the child->parent resolver over the
// parent-child edges.
func TestLoadParentIDsForChildrenInTx(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)
	mock.ExpectQuery(`SELECT issue_id, .* AS depends_on_id FROM dependencies`).WithArgs("bd-child").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).AddRow("bd-child", "bd-parent"))
	got, err := loadParentIDsForChildrenInTx(context.Background(), tx, []string{"dependencies"}, []string{"bd-child"})
	if err != nil {
		t.Fatalf("loadParentIDsForChildrenInTx: %v", err)
	}
	if got["bd-child"] != "bd-parent" {
		t.Fatalf("map = %v, want bd-child -> bd-parent", got)
	}
}

// TestGetDescendantIDsInTx covers the recursive parent-child descendant walk:
// the empty-root short-circuit, the happy path, the wisp-table fallback, and
// the max-depth guard.
func TestGetDescendantIDsInTx(t *testing.T) {
	t.Parallel()

	recursiveQ := `(?s)WITH RECURSIVE.*SELECT id, depth FROM descendants WHERE id <> `

	t.Run("empty root short-circuits", func(t *testing.T) {
		_, _, tx := beginMockTx(t)
		got, err := GetDescendantIDsInTx(context.Background(), tx, "", 0)
		if err != nil || got != nil {
			t.Fatalf("empty root: got %v, %v; want nil, nil", got, err)
		}
	})

	t.Run("returns descendants", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(recursiveQ).WillReturnRows(
			sqlmock.NewRows([]string{"id", "depth"}).AddRow("bd-c1", 1).AddRow("bd-c2", 2))
		got, err := GetDescendantIDsInTx(context.Background(), tx, "bd-root", 0)
		if err != nil {
			t.Fatalf("GetDescendantIDsInTx: %v", err)
		}
		if len(got) != 2 || got[0] != "bd-c1" || got[1] != "bd-c2" {
			t.Fatalf("descendants = %v, want [bd-c1 bd-c2]", got)
		}
	})

	t.Run("falls back to issues-only when wisp table missing", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		// First (with-wisps UNION) query fails table-missing; retry succeeds.
		mock.ExpectQuery(recursiveQ).WillReturnError(tableMissingErr)
		mock.ExpectQuery(recursiveQ).WillReturnRows(
			sqlmock.NewRows([]string{"id", "depth"}).AddRow("bd-c1", 1))
		got, err := GetDescendantIDsInTx(context.Background(), tx, "bd-root", 0)
		if err != nil {
			t.Fatalf("GetDescendantIDsInTx: %v", err)
		}
		if len(got) != 1 || got[0] != "bd-c1" {
			t.Fatalf("descendants = %v, want [bd-c1]", got)
		}
	})

	t.Run("max-depth reached errors", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(recursiveQ).WillReturnRows(
			sqlmock.NewRows([]string{"id", "depth"}).AddRow("bd-c1", 2))
		if _, err := GetDescendantIDsInTx(context.Background(), tx, "bd-root", 2); err == nil ||
			!strings.Contains(err.Error(), "reached max depth") {
			t.Fatalf("expected max-depth error, got %v", err)
		}
	})
}

// TestGetBlockedIssuesInTx covers the entry branches of the blocked-issue
// query: no blocked rows returns nil early, and a hard read error on the
// required issues table propagates. (The full expansion path fans out into
// loadStatusByID/GetIssuesByIDs and is exercised by the DB-backed suite.)
func TestGetBlockedIssuesInTx(t *testing.T) {
	t.Parallel()

	blockedQ := `SELECT id FROM issues\s+WHERE is_blocked = 1`
	wispBlockedQ := `SELECT id FROM wisps\s+WHERE is_blocked = 1`

	t.Run("no blocked rows returns nil", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(blockedQ).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectQuery(wispBlockedQ).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		got, err := GetBlockedIssuesInTx(context.Background(), tx, types.WorkFilter{})
		if err != nil {
			t.Fatalf("GetBlockedIssuesInTx: %v", err)
		}
		if got != nil {
			t.Fatalf("got %v, want nil for no blocked rows", got)
		}
	})

	t.Run("wisps table missing is tolerated, still nil", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(blockedQ).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectQuery(wispBlockedQ).WillReturnError(tableMissingErr)
		got, err := GetBlockedIssuesInTx(context.Background(), tx, types.WorkFilter{})
		if err != nil {
			t.Fatalf("GetBlockedIssuesInTx: %v", err)
		}
		if got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})

	t.Run("hard read error on issues table propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(blockedQ).WillReturnError(errors.New("boom"))
		if _, err := GetBlockedIssuesInTx(context.Background(), tx, types.WorkFilter{}); err == nil {
			t.Fatal("expected error on issues-table failure, got nil")
		}
	})
}
