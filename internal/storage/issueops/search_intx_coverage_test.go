package issueops

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// These tests cover the SearchIssuesInTx path and its helpers (searchTableInTx
// Pattern A/B, hydrateIssues, ephemeral routing, and the wisps merge) using
// sqlmock — hermetic, no live Dolt. This is the OOM/correctness-critical
// unbounded-search read path (beads-heq class). The default sqlmock QueryMatcher
// is regexp/partial, so query expectations match on stable substrings rather
// than the full 47-column projection.

// issueRow returns a single-row result set matching IssueSelectColumns with the
// given id/title and the rest zero/NULL. Enough for ScanIssueFrom to succeed.
// It reuses the package-local issueColumns()/issueRowValues() helpers so the
// column layout stays in sync with the canonical projection.
func issueRow(id, title string) *sqlmock.Rows {
	return sqlmock.NewRows(issueColumns()).AddRow(issueRowValues(id, title)...)
}

// TestSearchIssuesInTx_PatternA covers the unlimited full-projection scan plus
// label hydration and the wisps-empty short-circuit (no wisp merge query).
func TestSearchIssuesInTx_PatternA(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	// Pattern A: full column scan on issues (no LIMIT since filter.Limit==0).
	mock.ExpectQuery(`FROM issues`).
		WillReturnRows(issueRow("bd-1", "first"))
	// Label hydration for the returned issue.
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN`).
		WithArgs("bd-1").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}).AddRow("bd-1", "coverage"))
	// Wisps merge branch: probe reports the wisps table empty → short-circuit.
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	got, err := SearchIssuesInTx(context.Background(), tx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssuesInTx: %v", err)
	}
	if len(got) != 1 || got[0].ID != "bd-1" {
		t.Fatalf("got %d issues (%v), want 1 [bd-1]", len(got), got)
	}
	if len(got[0].Labels) != 1 || got[0].Labels[0] != "coverage" {
		t.Errorf("labels = %v, want [coverage]", got[0].Labels)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestSearchIssuesInTx_SkipWisps covers the SkipWisps escape hatch: no wisps
// probe or merge query is issued.
func TestSearchIssuesInTx_SkipWisps(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`FROM issues`).
		WillReturnRows(issueRow("bd-2", "solo"))
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN`).
		WithArgs("bd-2").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))

	got, err := SearchIssuesInTx(context.Background(), tx, "", types.IssueFilter{SkipWisps: true})
	if err != nil {
		t.Fatalf("SearchIssuesInTx: %v", err)
	}
	if len(got) != 1 || got[0].ID != "bd-2" {
		t.Fatalf("got %v, want [bd-2]", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestSearchIssuesInTx_PatternB covers the id-shrunk path (filter.Limit>0):
// a cheap SELECT id scan, batch hydration by IN(...), then label hydration.
func TestSearchIssuesInTx_PatternB(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	// Pattern B step 1: id-only scan with LIMIT.
	mock.ExpectQuery(`SELECT .*id FROM issues.*LIMIT 5`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-3"))
	// Pattern B step 2: batch fetch full rows by id.
	mock.ExpectQuery(`SELECT .* FROM issues WHERE id IN`).
		WithArgs("bd-3").
		WillReturnRows(issueRow("bd-3", "limited"))
	// Label hydration.
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN`).
		WithArgs("bd-3").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
	// Wisps merge: probe empty → short-circuit.
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	got, err := SearchIssuesInTx(context.Background(), tx, "", types.IssueFilter{Limit: 5})
	if err != nil {
		t.Fatalf("SearchIssuesInTx: %v", err)
	}
	if len(got) != 1 || got[0].ID != "bd-3" {
		t.Fatalf("got %v, want [bd-3]", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestSearchIssuesInTx_WispMergeDedup covers the merge branch where the wisps
// table is non-empty and returns a row that shadows an issues-table row of the
// same ID (the wisp record wins).
func TestSearchIssuesInTx_WispMergeDedup(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	// issues scan returns bd-1 and bd-2.
	mock.ExpectQuery(`FROM issues`).
		WillReturnRows(issueRow("bd-1", "issue-1").AddRow(issueRowValues("bd-2", "issue-2")...))
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
	// wisps non-empty probe.
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	// wisps scan returns bd-2 (shadows the issues bd-2) + hydration.
	mock.ExpectQuery(`FROM wisps`).
		WillReturnRows(issueRow("bd-2", "wisp-2"))
	mock.ExpectQuery(`SELECT issue_id, label FROM wisp_labels WHERE issue_id IN`).
		WithArgs("bd-2").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))

	got, err := SearchIssuesInTx(context.Background(), tx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssuesInTx: %v", err)
	}
	// bd-1 (issue) + bd-2 (wisp wins) = 2, and bd-2's title is the wisp's.
	byID := map[string]string{}
	for _, iss := range got {
		byID[iss.ID] = iss.Title
	}
	if len(got) != 2 {
		t.Fatalf("got %d issues, want 2 (%v)", len(got), byID)
	}
	if byID["bd-2"] != "wisp-2" {
		t.Errorf("bd-2 title = %q, want wisp-2 (wisp record should win)", byID["bd-2"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestSearchIssuesInTx_EphemeralRouting covers the Ephemeral=&true branch that
// routes to the wisps table and returns directly when results are present.
func TestSearchIssuesInTx_EphemeralRouting(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	ephemeral := true
	mock.ExpectQuery(`FROM wisps`).
		WillReturnRows(issueRow("bd-w", "ephemeral"))
	mock.ExpectQuery(`SELECT issue_id, label FROM wisp_labels WHERE issue_id IN`).
		WithArgs("bd-w").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))

	got, err := SearchIssuesInTx(context.Background(), tx, "", types.IssueFilter{Ephemeral: &ephemeral})
	if err != nil {
		t.Fatalf("SearchIssuesInTx: %v", err)
	}
	if len(got) != 1 || got[0].ID != "bd-w" {
		t.Fatalf("got %v, want [bd-w]", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestSearchIssuesInTx_IssuesQueryError covers the error-return path when the
// primary issues-table scan fails.
func TestSearchIssuesInTx_IssuesQueryError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`FROM issues`).
		WillReturnError(errors.New("boom"))

	_, err := SearchIssuesInTx(context.Background(), tx, "", types.IssueFilter{})
	if err == nil {
		t.Fatal("SearchIssuesInTx err = nil, want an error")
	}
}

// TestSearchIssuesInTx_WispProbeError covers the error path when the
// wisps-empty probe itself fails during the merge branch.
func TestSearchIssuesInTx_WispProbeError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`FROM issues`).
		WillReturnRows(issueRow("bd-1", "first"))
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN`).
		WithArgs("bd-1").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnError(errors.New("probe failed"))

	_, err := SearchIssuesInTx(context.Background(), tx, "", types.IssueFilter{})
	if err == nil {
		t.Fatal("SearchIssuesInTx err = nil, want a probe error")
	}
}
