package issueops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestMarkBlockedTemplateForIssues asserts the issue mark-template targets the
// issues table, gates on is_blocked=0 + open status, embeds the shared
// waits-for gate, and keys every EXISTS leg off the row's own id.
func TestMarkBlockedTemplateForIssues(t *testing.T) {
	t.Parallel()

	got := markBlockedTemplateForIssues()

	for _, want := range []string{
		"UPDATE issues i SET i.is_blocked = 1, i.updated_at = i.updated_at",
		"WHERE i.id IN (%s)",
		"AND i.is_blocked = 0",
		"AND i.status <> 'closed' AND i.status <> 'pinned'",
		"FROM dependencies d",
		"JOIN issues t ON t.id = d.depends_on_issue_id",
		"JOIN wisps t ON t.id = d.depends_on_wisp_id",
		"d.type = 'blocks' OR d.type = 'conditional-blocks'",
		"d.type = 'parent-child'",
		"d.type = 'waits-for'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mark-issues template missing %q\nfull:\n%s", want, got)
		}
	}

	if !strings.Contains(got, waitsForGateBlockedSQL) {
		t.Error("mark-issues template did not embed waitsForGateBlockedSQL")
	}

	// Exactly one %s IN-clause placeholder remains for the batched runner.
	if n := strings.Count(got, "IN (%s)"); n != 1 {
		t.Errorf("mark-issues template has %d IN(%%s) placeholders, want 1", n)
	}

	// Five EXISTS legs each keyed off the row alias.
	if n := strings.Count(got, "WHERE d.issue_id = i.id"); n != 5 {
		t.Errorf("mark-issues template has %d legs keyed off i.id, want 5", n)
	}
}

// TestUnmarkBlockedTemplateForIssues asserts the unmark-template targets the
// issues table, gates on is_blocked=1, and clears the flag when the row is
// closed/pinned OR none of the negated blocker legs hold.
func TestUnmarkBlockedTemplateForIssues(t *testing.T) {
	t.Parallel()

	got := unmarkBlockedTemplateForIssues()

	for _, want := range []string{
		"UPDATE issues i SET i.is_blocked = 0, i.updated_at = i.updated_at",
		"WHERE i.id IN (%s)",
		"AND i.is_blocked = 1",
		"i.status = 'closed' OR i.status = 'pinned'",
		"NOT EXISTS (",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unmark-issues template missing %q\nfull:\n%s", want, got)
		}
	}

	if !strings.Contains(got, waitsForGateBlockedSQL) {
		t.Error("unmark-issues template did not embed waitsForGateBlockedSQL")
	}

	// The clear path negates all five blocker legs.
	if n := strings.Count(got, "NOT EXISTS ("); n != 5 {
		t.Errorf("unmark-issues template has %d NOT EXISTS legs, want 5", n)
	}
}

// TestMarkBlockedTemplateForWisps asserts the wisp mark-template targets the
// wisps table via wisp_dependencies (not the issues dep table).
func TestMarkBlockedTemplateForWisps(t *testing.T) {
	t.Parallel()

	got := markBlockedTemplateForWisps()

	for _, want := range []string{
		"UPDATE wisps w SET w.is_blocked = 1, w.updated_at = w.updated_at",
		"WHERE w.id IN (%s)",
		"AND w.is_blocked = 0",
		"FROM wisp_dependencies d",
		"WHERE d.issue_id = w.id",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mark-wisps template missing %q\nfull:\n%s", want, got)
		}
	}

	// The wisp path drives its edges off wisp_dependencies, never the plain
	// issues dependencies table.
	if strings.Contains(got, "FROM dependencies d") {
		t.Error("mark-wisps template must not read the issues dependencies table")
	}
	if !strings.Contains(got, waitsForGateBlockedSQL) {
		t.Error("mark-wisps template did not embed waitsForGateBlockedSQL")
	}
}

// TestUnmarkBlockedTemplateForWisps asserts the wisp unmark-template targets
// the wisps table via wisp_dependencies with the negated blocker legs.
func TestUnmarkBlockedTemplateForWisps(t *testing.T) {
	t.Parallel()

	got := unmarkBlockedTemplateForWisps()

	for _, want := range []string{
		"UPDATE wisps w SET w.is_blocked = 0, w.updated_at = w.updated_at",
		"WHERE w.id IN (%s)",
		"AND w.is_blocked = 1",
		"w.status = 'closed' OR w.status = 'pinned'",
		"FROM wisp_dependencies d",
		"NOT EXISTS (",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unmark-wisps template missing %q\nfull:\n%s", want, got)
		}
	}

	if strings.Contains(got, "FROM dependencies d") {
		t.Error("unmark-wisps template must not read the issues dependencies table")
	}
	if !strings.Contains(got, waitsForGateBlockedSQL) {
		t.Error("unmark-wisps template did not embed waitsForGateBlockedSQL")
	}
}

// TestRunMarkBatchedInTx exercises the mark-only batched runner: it sums
// RowsAffected across batches and propagates an Exec error mid-loop.
func TestRunMarkBatchedInTx(t *testing.T) {
	t.Parallel()

	tmpl := "UPDATE issues SET is_blocked = 1 WHERE id IN (%s)"

	t.Run("sums rows affected", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("UPDATE issues SET is_blocked = 1").
			WithArgs("bd-1", "bd-2").
			WillReturnResult(sqlmock.NewResult(0, 3))
		n, err := runMarkBatchedInTx(context.Background(), tx, tmpl, []string{"bd-1", "bd-2"})
		if err != nil {
			t.Fatalf("runMarkBatchedInTx: %v", err)
		}
		if n != 3 {
			t.Fatalf("changed = %d, want 3", n)
		}
	})

	t.Run("propagates exec error", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("UPDATE issues SET is_blocked = 1").
			WithArgs("bd-1").
			WillReturnError(errors.New("boom"))
		if _, err := runMarkBatchedInTx(context.Background(), tx, tmpl, []string{"bd-1"}); err == nil ||
			!strings.Contains(err.Error(), "mark is_blocked") {
			t.Fatalf("expected wrapped mark error, got %v", err)
		}
	})
}

// TestRunMarkUnmarkBatchedInTx exercises the mark+unmark batched runner: it
// sums both statements' RowsAffected, and propagates an error from either the
// mark or the unmark leg.
func TestRunMarkUnmarkBatchedInTx(t *testing.T) {
	t.Parallel()

	markTmpl := "UPDATE issues SET is_blocked = 1 WHERE id IN (%s)"
	unmarkTmpl := "UPDATE issues SET is_blocked = 0 WHERE id IN (%s)"

	t.Run("sums both legs", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("UPDATE issues SET is_blocked = 1").
			WithArgs("bd-1").WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectExec("UPDATE issues SET is_blocked = 0").
			WithArgs("bd-1").WillReturnResult(sqlmock.NewResult(0, 1))
		n, err := runMarkUnmarkBatchedInTx(context.Background(), tx, markTmpl, unmarkTmpl, []string{"bd-1"})
		if err != nil {
			t.Fatalf("runMarkUnmarkBatchedInTx: %v", err)
		}
		if n != 3 {
			t.Fatalf("changed = %d, want 3 (2 mark + 1 unmark)", n)
		}
	})

	t.Run("propagates mark error", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("UPDATE issues SET is_blocked = 1").
			WithArgs("bd-1").WillReturnError(errors.New("boom"))
		if _, err := runMarkUnmarkBatchedInTx(context.Background(), tx, markTmpl, unmarkTmpl, []string{"bd-1"}); err == nil ||
			!strings.Contains(err.Error(), "recompute is_blocked (mark)") {
			t.Fatalf("expected wrapped mark error, got %v", err)
		}
	})

	t.Run("propagates unmark error", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("UPDATE issues SET is_blocked = 1").
			WithArgs("bd-1").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("UPDATE issues SET is_blocked = 0").
			WithArgs("bd-1").WillReturnError(errors.New("boom"))
		if _, err := runMarkUnmarkBatchedInTx(context.Background(), tx, markTmpl, unmarkTmpl, []string{"bd-1"}); err == nil ||
			!strings.Contains(err.Error(), "recompute is_blocked (unmark)") {
			t.Fatalf("expected wrapped unmark error, got %v", err)
		}
	})
}

// TestBlockedPassEmptyShortCircuits asserts the four per-table pass wrappers
// short-circuit to (0, nil) on empty input without touching the tx.
func TestBlockedPassEmptyShortCircuits(t *testing.T) {
	t.Parallel()

	_, _, tx := beginMockTx(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		fn   func() (int64, error)
	}{
		{"recompute issues", func() (int64, error) { return recomputeIsBlockedPassForIssuesInTx(ctx, tx, nil) }},
		{"mark issues", func() (int64, error) { return markIsBlockedPassForIssuesInTx(ctx, tx, nil) }},
		{"recompute wisps", func() (int64, error) { return recomputeIsBlockedPassForWispsInTx(ctx, tx, nil) }},
		{"mark wisps", func() (int64, error) { return markIsBlockedPassForWispsInTx(ctx, tx, nil) }},
	} {
		n, err := tc.fn()
		if err != nil || n != 0 {
			t.Errorf("%s empty: got (%d, %v), want (0, nil)", tc.name, n, err)
		}
	}
}

// TestRecomputeMarkIsBlockedInTxEmpty asserts the top-level fixpoint loops
// short-circuit when both id slices are empty (no tx interaction).
func TestRecomputeMarkIsBlockedInTxEmpty(t *testing.T) {
	t.Parallel()

	_, _, tx := beginMockTx(t)
	ctx := context.Background()

	if err := RecomputeIsBlockedInTx(ctx, tx, nil, nil); err != nil {
		t.Errorf("RecomputeIsBlockedInTx empty: %v", err)
	}
	if err := MarkIsBlockedInTx(ctx, tx, nil, nil); err != nil {
		t.Errorf("MarkIsBlockedInTx empty: %v", err)
	}
}
