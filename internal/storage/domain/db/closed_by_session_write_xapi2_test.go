package db

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/steveyegge/beads/internal/types"
)

// beads-xapi2 (WRITE twin of ni2ph, domain/db chokepoint): insertIssueRow is
// the domain/proxied-UOW hand-written INSERT ... ON DUPLICATE KEY UPDATE for
// issues/wisps. ni2ph made closed_by_session hydrate + JSON-export on the READ
// side, but this INSERT column list and its UPSERT set both had close_reason
// and omitted closed_by_session, so a closed issue imported through the proxied
// create path dropped the session (federated OUT but not IN). This is a
// separate write chokepoint from issueops.insertIssueIntoTable (covered by the
// dolt-package cgo teeth) — the two hand-written INSERTs must both carry it.
//
// These teeth assert the emitted SQL includes closed_by_session in BOTH the
// column list and the ON DUPLICATE KEY UPDATE set, and that the session value
// is bound as an argument. RED before the fix (column absent, value never
// passed); no embedded Dolt needed — the SQL text + arg binding is the contract.
func TestInsertIssueRow_CarriesClosedBySession_xapi2(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	closedAt := time.Now().UTC()
	issue := &types.Issue{
		ID:              "cbs-domain",
		Title:           "imported closed issue",
		Status:          types.StatusClosed,
		Priority:        2,
		IssueType:       types.TypeTask,
		CloseReason:     "done",
		ClosedBySession: "sess-DOMAIN-7",
		ClosedAt:        &closedAt,
	}

	// The column must appear both in the INSERT column list and the UPSERT set.
	// The default sqlmock matcher treats the expected string as a regexp, so
	// match the literal INSERT-list occurrence followed by the UPSERT assignment
	// (VALUES(...) parens escaped).
	mock.ExpectExec("closed_by_session,[\\s\\S]*closed_by_session = VALUES\\(closed_by_session\\)").
		WithArgs(closedBySessionArgs()...).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := insertIssueRow(context.Background(), db, "issues", issue); err != nil {
		t.Fatalf("insertIssueRow: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		// A mismatch here means either the column dropped out of the SQL or the
		// session value was not bound as an argument.
		t.Fatalf("SQL/arg contract not met (closed_by_session missing from INSERT or UPSERT): %v", err)
	}
}

// closedBySessionArgs pins the 47 positional bind args: every slot is AnyArg
// except the closed_by_session slot, which must be "sess-DOMAIN-7". In the
// INSERT list closed_by_session immediately follows close_reason, so it is the
// 36th column (1-indexed) => arg index 35.
func closedBySessionArgs() []driver.Value {
	m := make([]driver.Value, 47)
	for i := range m {
		m[i] = sqlmock.AnyArg()
	}
	m[35] = "sess-DOMAIN-7"
	return m
}
