package issueops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage"
)

// These tests cover AsOfInTx using sqlmock — hermetic, no live Dolt. AsOfInTx
// validates the ref, then issues a single AS OF SELECT of 14 columns and
// hydrates the nullable fields. The default sqlmock QueryMatcher is
// regexp/partial, so expectations match on stable substrings.

// asOfCols is the 14-column projection AsOfInTx scans, in order.
var asOfCols = []string{
	"id", "content_hash", "title", "description", "status", "priority", "issue_type", "assignee", "estimated_minutes",
	"created_at", "created_by", "owner", "updated_at", "closed_at",
}

// TestAsOfInTx_InvalidRef: an invalid ref is rejected before any query runs.
func TestAsOfInTx_InvalidRef(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	// No ExpectQuery — validation must short-circuit before the DB is touched.
	_, err := AsOfInTx(context.Background(), tx, "bd-1", "bad ref with spaces")
	if err == nil {
		t.Fatal("err = nil, want invalid-ref error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (no query should have run): %v", err)
	}
}

// TestAsOfInTx_FullRow: every nullable column is present and hydrated onto the
// returned issue.
func TestAsOfInTx_FullRow(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`FROM issues AS OF`).
		WithArgs("bd-1").
		WillReturnRows(sqlmock.NewRows(asOfCols).AddRow(
			"bd-1", "hash123", "the title", "the desc", "open", 2, "task", "alice", int64(45),
			"2026-07-17T00:00:00Z", "bob", "carol", "2026-07-17T01:00:00Z", time.Now(),
		))

	got, err := AsOfInTx(context.Background(), tx, "bd-1", "release/v2.0")
	if err != nil {
		t.Fatalf("AsOfInTx: %v", err)
	}
	if got.ID != "bd-1" || got.Title != "the title" {
		t.Errorf("id/title = %q/%q, want bd-1/the title", got.ID, got.Title)
	}
	if got.ContentHash != "hash123" {
		t.Errorf("content_hash = %q, want hash123", got.ContentHash)
	}
	if got.Assignee != "alice" {
		t.Errorf("assignee = %q, want alice", got.Assignee)
	}
	if got.Owner != "carol" {
		t.Errorf("owner = %q, want carol", got.Owner)
	}
	if got.EstimatedMinutes == nil || *got.EstimatedMinutes != 45 {
		t.Errorf("estimated_minutes = %v, want 45", got.EstimatedMinutes)
	}
	if got.ClosedAt == nil {
		t.Error("closed_at = nil, want a populated time")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("created_at/updated_at should be parsed; got %v/%v", got.CreatedAt, got.UpdatedAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestAsOfInTx_NullFields: all nullable columns NULL → the corresponding issue
// fields stay at their zero values (no panics, no spurious hydration).
func TestAsOfInTx_NullFields(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`FROM issues AS OF`).
		WithArgs("bd-2").
		WillReturnRows(sqlmock.NewRows(asOfCols).AddRow(
			"bd-2", nil, "t2", "d2", "open", 1, "bug", nil, nil,
			nil, "creator", nil, nil, nil,
		))

	got, err := AsOfInTx(context.Background(), tx, "bd-2", "main")
	if err != nil {
		t.Fatalf("AsOfInTx: %v", err)
	}
	if got.ContentHash != "" || got.Assignee != "" || got.Owner != "" {
		t.Errorf("nullable strings should stay empty; got hash=%q assignee=%q owner=%q",
			got.ContentHash, got.Assignee, got.Owner)
	}
	if got.EstimatedMinutes != nil {
		t.Errorf("estimated_minutes = %v, want nil", got.EstimatedMinutes)
	}
	if got.ClosedAt != nil {
		t.Errorf("closed_at = %v, want nil", got.ClosedAt)
	}
	if !got.CreatedAt.IsZero() || !got.UpdatedAt.IsZero() {
		t.Errorf("null time strings should leave zero times; got %v/%v", got.CreatedAt, got.UpdatedAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestAsOfInTx_NotFound: no row maps to storage.ErrNotFound.
func TestAsOfInTx_NotFound(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`FROM issues AS OF`).
		WithArgs("bd-missing").
		WillReturnRows(sqlmock.NewRows(asOfCols))

	_, err := AsOfInTx(context.Background(), tx, "bd-missing", "main")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err = %v, want storage.ErrNotFound", err)
	}
}

// TestAsOfInTx_QueryError: a generic scan/query error is wrapped and returned.
func TestAsOfInTx_QueryError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	mock.ExpectQuery(`FROM issues AS OF`).
		WithArgs("bd-3").
		WillReturnError(errors.New("as-of boom"))

	_, err := AsOfInTx(context.Background(), tx, "bd-3", "main")
	if err == nil {
		t.Fatal("err = nil, want a query error")
	}
	if errors.Is(err, storage.ErrNotFound) {
		t.Errorf("err = %v, should NOT be ErrNotFound for a generic failure", err)
	}
}
