package issueops

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// These tests cover ClaimIssueInTx (CAS claim) and ClaimReadyIssueInTx using
// sqlmock — hermetic, no live Dolt. ClaimIssueInTx routes through
// IsActiveWispInTx (wisps probe) → GetIssueInTx (issues/wisps select + label
// hydration) → conditional UPDATE → rowsAffected branches → event insert. The
// default sqlmock QueryMatcher is regexp/partial, so expectations match on
// stable substrings.

// issueRowValuesStarted is like issueRowValues but sets started_at to a valid
// timestamp so the scanned issue's StartedAt is non-nil (exercises the
// "already started" UPDATE branch that omits started_at).
func issueRowValuesStarted(id, title string) []driver.Value {
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
		case "started_at":
			values = append(values, time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC))
		default:
			values = append(values, nil)
		}
	}
	return values
}

// expectClaimLookup queues the wisps probe (miss → not a wisp) + the GetIssueInTx
// issues select + label hydration for a fresh open issue with the given
// started_at behavior.
func expectClaimLookup(mock sqlmock.Sqlmock, id string, started bool) {
	// IsActiveWispInTx probe: not a wisp.
	mock.ExpectQuery(`SELECT 1 FROM wisps WHERE id = \? LIMIT 1`).
		WithArgs(id).
		WillReturnError(errors.New("not a wisp"))
	// GetIssueInTx: select from issues table.
	vals := issueRowValues(id, "t")
	if started {
		vals = issueRowValuesStarted(id, "t")
	}
	mock.ExpectQuery(`FROM issues WHERE id = \?`).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows(issueColumns()).AddRow(vals...))
	// Label hydration from labels table.
	mock.ExpectQuery(`SELECT label FROM labels WHERE issue_id = \?`).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"label"}))
}

// TestClaimIssueInTx_FreshClaim covers the happy path where started_at is NULL
// (first transition), the conditional UPDATE affects one row, and the claim
// event is recorded.
func TestClaimIssueInTx_FreshClaim(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectClaimLookup(mock, "bd-1", false)
	// UPDATE with started_at (StartedAt was nil).
	mock.ExpectExec(`UPDATE issues\s+SET assignee = \?, status = 'in_progress', updated_at = \?, started_at = \?`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Event insert.
	mock.ExpectExec(`INSERT INTO events`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := ClaimIssueInTx(context.Background(), tx, "bd-1", "alice")
	if err != nil {
		t.Fatalf("ClaimIssueInTx: %v", err)
	}
	if res == nil || res.OldIssue == nil || res.OldIssue.ID != "bd-1" || res.IsWisp {
		t.Fatalf("unexpected result: %+v", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimIssueInTx_AlreadyStarted covers the UPDATE branch that omits
// started_at because the issue already has a start time.
func TestClaimIssueInTx_AlreadyStarted(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectClaimLookup(mock, "bd-2", true)
	// UPDATE without started_at (StartedAt was non-nil).
	mock.ExpectExec(`UPDATE issues\s+SET assignee = \?, status = 'in_progress', updated_at = \?\s+WHERE`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO events`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := ClaimIssueInTx(context.Background(), tx, "bd-2", "bob")
	if err != nil {
		t.Fatalf("ClaimIssueInTx: %v", err)
	}
	if res == nil || res.OldIssue.ID != "bd-2" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimIssueInTx_IdempotentReclaim covers rowsAffected==0 where the issue is
// already in_progress by the same actor → success (agent retry).
func TestClaimIssueInTx_IdempotentReclaim(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectClaimLookup(mock, "bd-3", false)
	mock.ExpectExec(`UPDATE issues`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// Post-UPDATE state query: same actor, in_progress.
	mock.ExpectQuery(`SELECT assignee, status FROM issues WHERE id = \?`).
		WithArgs("bd-3").
		WillReturnRows(sqlmock.NewRows([]string{"assignee", "status"}).
			AddRow("alice", string(types.StatusInProgress)))

	res, err := ClaimIssueInTx(context.Background(), tx, "bd-3", "alice")
	if err != nil {
		t.Fatalf("ClaimIssueInTx idempotent: %v", err)
	}
	if res == nil || res.OldIssue.ID != "bd-3" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimIssueInTx_AlreadyClaimedByOther covers rowsAffected==0 where the
// issue is assigned to a different actor → ErrAlreadyClaimed.
func TestClaimIssueInTx_AlreadyClaimedByOther(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectClaimLookup(mock, "bd-4", false)
	mock.ExpectExec(`UPDATE issues`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT assignee, status FROM issues WHERE id = \?`).
		WithArgs("bd-4").
		WillReturnRows(sqlmock.NewRows([]string{"assignee", "status"}).
			AddRow("carol", string(types.StatusInProgress)))

	_, err := ClaimIssueInTx(context.Background(), tx, "bd-4", "alice")
	if !errors.Is(err, storage.ErrAlreadyClaimed) {
		t.Fatalf("want ErrAlreadyClaimed, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimIssueInTx_NotClaimable covers rowsAffected==0 where the issue is
// unassigned but not open (e.g. closed) → ErrNotClaimable.
func TestClaimIssueInTx_NotClaimable(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectClaimLookup(mock, "bd-5", false)
	mock.ExpectExec(`UPDATE issues`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT assignee, status FROM issues WHERE id = \?`).
		WithArgs("bd-5").
		WillReturnRows(sqlmock.NewRows([]string{"assignee", "status"}).
			AddRow("", string(types.StatusClosed)))

	_, err := ClaimIssueInTx(context.Background(), tx, "bd-5", "alice")
	if !errors.Is(err, storage.ErrNotClaimable) {
		t.Fatalf("want ErrNotClaimable, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimIssueInTx_GetIssueError covers the early failure when GetIssueInTx
// cannot load the issue for event recording.
func TestClaimIssueInTx_GetIssueError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`SELECT 1 FROM wisps WHERE id = \? LIMIT 1`).
		WithArgs("bd-6").
		WillReturnError(errors.New("not a wisp"))
	// issues select fails hard (not ErrNoRows) → GetIssueInTx returns error.
	mock.ExpectQuery(`FROM issues WHERE id = \?`).
		WithArgs("bd-6").
		WillReturnError(errors.New("boom"))

	_, err := ClaimIssueInTx(context.Background(), tx, "bd-6", "alice")
	if err == nil {
		t.Fatal("want error from GetIssueInTx, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimIssueInTx_UpdateError covers the failure of the conditional UPDATE.
func TestClaimIssueInTx_UpdateError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectClaimLookup(mock, "bd-7", false)
	mock.ExpectExec(`UPDATE issues`).
		WillReturnError(errors.New("update failed"))

	_, err := ClaimIssueInTx(context.Background(), tx, "bd-7", "alice")
	if err == nil {
		t.Fatal("want error from UPDATE, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimIssueInTx_StateQueryError covers the failure to query current state
// after a zero-row UPDATE.
func TestClaimIssueInTx_StateQueryError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectClaimLookup(mock, "bd-8", false)
	mock.ExpectExec(`UPDATE issues`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT assignee, status FROM issues WHERE id = \?`).
		WithArgs("bd-8").
		WillReturnError(errors.New("state query failed"))

	_, err := ClaimIssueInTx(context.Background(), tx, "bd-8", "alice")
	if err == nil {
		t.Fatal("want error from state query, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimIssueInTx_EventError covers the failure of the claim-event insert.
func TestClaimIssueInTx_EventError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectClaimLookup(mock, "bd-9", false)
	mock.ExpectExec(`UPDATE issues`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO events`).
		WillReturnError(errors.New("event insert failed"))

	_, err := ClaimIssueInTx(context.Background(), tx, "bd-9", "alice")
	if err == nil {
		t.Fatal("want error from event insert, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimReadyIssueInTx_NoReadyWork covers the success path where the ready
// query yields no issues and the wisps table is empty, so the claim loop runs
// zero iterations and returns (nil, nil).
func TestClaimReadyIssueInTx_NoReadyWork(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	// getChildrenOfDeferredParentsInTx: no deferred parents in issues or wisps.
	mock.ExpectQuery(`FROM issues\s+WHERE defer_until IS NOT NULL`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM wisps\s+WHERE defer_until IS NOT NULL`).
		WillReturnError(sql.ErrNoRows)
	// Main ready query: no rows.
	mock.ExpectQuery(`SELECT id FROM issues`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// getReadyWispsInTx probe: wisps table empty → short-circuit.
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnError(sql.ErrNoRows)

	got, err := ClaimReadyIssueInTx(context.Background(), tx, types.WorkFilter{}, "alice")
	if err != nil {
		t.Fatalf("ClaimReadyIssueInTx: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil issue when nothing ready, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimReadyIssueInTx_ClaimsFirstReady covers the success loop body: the
// ready query yields one issue, ClaimIssueInTx succeeds, and the freshly
// claimed issue is re-read and returned.
func TestClaimReadyIssueInTx_ClaimsFirstReady(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	// getChildrenOfDeferredParentsInTx: no deferred parents.
	mock.ExpectQuery(`FROM issues\s+WHERE defer_until IS NOT NULL`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM wisps\s+WHERE defer_until IS NOT NULL`).
		WillReturnError(sql.ErrNoRows)
	// Main ready query yields one id.
	mock.ExpectQuery(`SELECT id FROM issues`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-10"))
	// GetIssuesByIDsInTx → WispIDSetInTx probe: wisps empty (all perms).
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnError(sql.ErrNoRows)
	// GetIssuesByIDsInTx: select the perm issue + label hydration.
	mock.ExpectQuery(`FROM issues WHERE id IN`).
		WithArgs("bd-10").
		WillReturnRows(sqlmock.NewRows(issueColumns()).AddRow(issueRowValues("bd-10", "t")...))
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN`).
		WithArgs("bd-10").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
	// getReadyWispsInTx probe: wisps empty → no wisp merge.
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnError(sql.ErrNoRows)

	// Loop: ClaimIssueInTx("bd-10").
	expectClaimLookup(mock, "bd-10", false)
	mock.ExpectExec(`UPDATE issues`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO events`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// Re-read the claimed issue (GetIssueInTx: issues select + labels, no probe).
	mock.ExpectQuery(`FROM issues WHERE id = \?`).
		WithArgs("bd-10").
		WillReturnRows(sqlmock.NewRows(issueColumns()).AddRow(issueRowValues("bd-10", "t")...))
	mock.ExpectQuery(`SELECT label FROM labels WHERE issue_id = \?`).
		WithArgs("bd-10").
		WillReturnRows(sqlmock.NewRows([]string{"label"}))

	got, err := ClaimReadyIssueInTx(context.Background(), tx, types.WorkFilter{}, "alice")
	if err != nil {
		t.Fatalf("ClaimReadyIssueInTx: %v", err)
	}
	if got == nil || got.ID != "bd-10" {
		t.Fatalf("want claimed bd-10, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimReadyIssueInTx_SkipsAlreadyClaimed covers the loop continue branch:
// the first ready issue is claimed by another actor (ErrAlreadyClaimed), so the
// loop skips it; with no further candidates it returns (nil, nil).
func TestClaimReadyIssueInTx_SkipsAlreadyClaimed(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`FROM issues\s+WHERE defer_until IS NOT NULL`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM wisps\s+WHERE defer_until IS NOT NULL`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT id FROM issues`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-11"))
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM issues WHERE id IN`).
		WithArgs("bd-11").
		WillReturnRows(sqlmock.NewRows(issueColumns()).AddRow(issueRowValues("bd-11", "t")...))
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN`).
		WithArgs("bd-11").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnError(sql.ErrNoRows)

	// Loop: ClaimIssueInTx("bd-11") → already claimed by someone else → skip.
	expectClaimLookup(mock, "bd-11", false)
	mock.ExpectExec(`UPDATE issues`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT assignee, status FROM issues WHERE id = \?`).
		WithArgs("bd-11").
		WillReturnRows(sqlmock.NewRows([]string{"assignee", "status"}).
			AddRow("carol", string(types.StatusInProgress)))

	got, err := ClaimReadyIssueInTx(context.Background(), tx, types.WorkFilter{}, "alice")
	if err != nil {
		t.Fatalf("ClaimReadyIssueInTx: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil after skipping already-claimed, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestClaimReadyIssueInTx_PropagatesReadyError covers the ClaimReadyIssueInTx
// path where GetReadyWorkInTx fails (the deferred-parent probe errors hard),
// which surfaces as an error from ClaimReadyIssueInTx.
func TestClaimReadyIssueInTx_PropagatesReadyError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	// getChildrenOfDeferredParentsInTx probes issues for deferred parents;
	// a hard (non-ErrNoRows, non-table-missing) error propagates up.
	mock.ExpectQuery(`FROM issues\s+WHERE defer_until IS NOT NULL`).
		WillReturnError(errors.New("deferred probe boom"))

	got, err := ClaimReadyIssueInTx(context.Background(), tx, types.WorkFilter{}, "alice")
	if err == nil {
		t.Fatal("want error propagated from GetReadyWorkInTx, got nil")
	}
	if got != nil {
		t.Fatalf("want nil issue on error, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
