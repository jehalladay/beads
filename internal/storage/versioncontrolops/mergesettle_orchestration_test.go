package versioncontrolops

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage"
)

// These tests cover the merge-settle orchestration in mergesettle.go —
// SettleMerge's decision tree, abortMerge's two fallbacks, CommitResolvedConflicts,
// and MergeConflictsError — via sqlmock, no live Dolt. The individual
// conflict-classification helpers (TryAutoResolveMergeConflicts,
// TryRepairFKCascadeViolations, dependency/config/schema classes) are covered by
// mergesettle_conflicts_test.go; here we drive the top-level flow that stitches
// them together and chooses settle-vs-abort.

// Query fragments SettleMerge issues, in order.
var (
	qConflicts  = regexp.QuoteMeta("SELECT `table`, num_conflicts FROM dolt_conflicts")
	qViolations = regexp.QuoteMeta("SELECT `table` FROM dolt_constraint_violations WHERE num_violations > 0")
	qAbort      = regexp.QuoteMeta("CALL DOLT_MERGE('--abort')")
	qHardReset  = regexp.QuoteMeta("CALL DOLT_RESET('--hard')")
	qCommit     = regexp.QuoteMeta("CALL DOLT_COMMIT")
)

func TestMergeConflictsError(t *testing.T) {
	t.Parallel()
	inner := errors.New("merge failed")
	e := &MergeConflictsError{
		Conflicts: []storage.Conflict{{Field: "issues"}, {Field: "labels"}},
		MergeErr:  inner,
	}
	msg := e.Error()
	if !strings.Contains(msg, "issues") || !strings.Contains(msg, "labels") {
		t.Errorf("Error() should list conflicted tables, got %q", msg)
	}
	if !strings.Contains(msg, "operator resolution") {
		t.Errorf("Error() should mention operator resolution, got %q", msg)
	}
	if !errors.Is(e, inner) {
		t.Error("Unwrap should expose the wrapped merge error via errors.Is")
	}
}

func TestCommitResolvedConflicts(t *testing.T) {
	t.Parallel()
	t.Run("happy path commits", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectExec(qCommit).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := CommitResolvedConflicts(context.Background(), db); err != nil {
			t.Fatalf("CommitResolvedConflicts: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("commit error is wrapped", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectExec(qCommit).WillReturnError(errors.New("dirty working set"))
		err := CommitResolvedConflicts(context.Background(), db)
		if err == nil || !strings.Contains(err.Error(), "commit resolved conflicts") {
			t.Errorf("expected wrapped commit error, got %v", err)
		}
	})
}

func TestAbortMerge(t *testing.T) {
	t.Parallel()
	t.Run("abort succeeds, no hard reset", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectExec(qAbort).WillReturnResult(sqlmock.NewResult(0, 0))
		abortMerge(context.Background(), db, true)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("abort fails + preMergeClean → hard reset fallback", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectExec(qAbort).WillReturnError(errors.New("no merge in progress"))
		mock.ExpectExec(qHardReset).WillReturnResult(sqlmock.NewResult(0, 0))
		abortMerge(context.Background(), db, true)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("abort fails + dirty working set → NO hard reset", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectExec(qAbort).WillReturnError(errors.New("no merge in progress"))
		// No hard-reset expectation: a dirty pre-merge set must not be wiped.
		abortMerge(context.Background(), db, false)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})
}

func TestSettleMerge_CleanNoConflicts(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	// 1) TryAutoResolveMergeConflicts: no conflicts → resolved=false, nil
	mock.ExpectQuery(qConflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	// 2) !resolved → GetConflicts: still empty → skip the conflict-error path
	mock.ExpectQuery(qConflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	// 3) TryRepairFKCascadeViolations: none
	mock.ExpectQuery(qViolations).WillReturnRows(sqlmock.NewRows([]string{"table"}))
	// mergeErr nil, nothing resolved/repaired → return nil (no commit, no abort)

	if err := SettleMerge(context.Background(), db, nil, true); err != nil {
		t.Fatalf("SettleMerge clean: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSettleMerge_ResolveErrorAborts(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	// TryAutoResolveMergeConflicts errors on the conflicts query.
	mock.ExpectQuery(qConflicts).WillReturnError(errors.New("conflicts query boom"))
	// → abortMerge (abort succeeds)
	mock.ExpectExec(qAbort).WillReturnResult(sqlmock.NewResult(0, 0))

	// mergeErr is nil, so the resolveErr is surfaced.
	err := SettleMerge(context.Background(), db, nil, true)
	if err == nil || !strings.Contains(err.Error(), "conflicts query boom") {
		t.Fatalf("expected resolve error surfaced, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSettleMerge_ResolveErrorPrefersMergeErr(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	mergeErr := errors.New("original merge failure")
	mock.ExpectQuery(qConflicts).WillReturnError(errors.New("conflicts query boom"))
	mock.ExpectExec(qAbort).WillReturnResult(sqlmock.NewResult(0, 0))

	// When both a mergeErr and a resolveErr exist, the merge error wins.
	err := SettleMerge(context.Background(), db, mergeErr, true)
	if !errors.Is(err, mergeErr) {
		t.Fatalf("expected the original merge error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSettleMerge_UnresolvedConflictsReturnMergeConflictsError(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	// TryAutoResolveMergeConflicts sees an unresolvable table ("issues" is not a
	// safe auto-resolve class) → declines → resolved=false, nil.
	mock.ExpectQuery(qConflicts).
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).AddRow("issues", 1))
	// !resolved → GetConflicts captures the conflicts before the abort.
	mock.ExpectQuery(qConflicts).
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).AddRow("issues", 1))
	// → abortMerge (abort succeeds)
	mock.ExpectExec(qAbort).WillReturnResult(sqlmock.NewResult(0, 0))

	err := SettleMerge(context.Background(), db, nil, true)
	var mce *MergeConflictsError
	if !errors.As(err, &mce) {
		t.Fatalf("expected *MergeConflictsError, got %T: %v", err, err)
	}
	if len(mce.Conflicts) == 0 {
		t.Error("MergeConflictsError should carry the captured conflicts")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSettleMerge_ResolvedConflictsCommit(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	qResolveMeta := regexp.QuoteMeta("CALL DOLT_CONFLICTS_RESOLVE('--theirs', 'metadata')")
	qAddMeta := regexp.QuoteMeta("CALL DOLT_ADD('metadata')")

	// 1) TryAutoResolveMergeConflicts: a metadata conflict is auto-resolvable.
	mock.ExpectQuery(qConflicts).
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).AddRow("metadata", 1))
	// resolve + stage the metadata table → resolved=true.
	mock.ExpectExec(qResolveMeta).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qAddMeta).WillReturnResult(sqlmock.NewResult(0, 0))
	// 2) resolved=true skips the GetConflicts path.
	// 3) TryRepairFKCascadeViolations: none.
	mock.ExpectQuery(qViolations).WillReturnRows(sqlmock.NewRows([]string{"table"}))
	// 4) resolved → CommitResolvedConflicts commits, returns nil.
	mock.ExpectExec(qCommit).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := SettleMerge(context.Background(), db, nil, true); err != nil {
		t.Fatalf("SettleMerge resolved-commit: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSettleMerge_ResolvedButCommitFailsAborts(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	qResolveMeta := regexp.QuoteMeta("CALL DOLT_CONFLICTS_RESOLVE('--theirs', 'metadata')")
	qAddMeta := regexp.QuoteMeta("CALL DOLT_ADD('metadata')")

	mock.ExpectQuery(qConflicts).
		WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).AddRow("metadata", 1))
	mock.ExpectExec(qResolveMeta).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qAddMeta).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(qViolations).WillReturnRows(sqlmock.NewRows([]string{"table"}))
	// CommitResolvedConflicts fails → abortMerge, and with nil mergeErr the
	// commit error is surfaced.
	mock.ExpectExec(qCommit).WillReturnError(errors.New("commit refused"))
	mock.ExpectExec(qAbort).WillReturnResult(sqlmock.NewResult(0, 0))

	err := SettleMerge(context.Background(), db, nil, true)
	if err == nil || !strings.Contains(err.Error(), "commit resolved conflicts") {
		t.Fatalf("expected wrapped commit error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSettleMerge_UnrepairableViolationAborts(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	// No conflicts to resolve.
	mock.ExpectQuery(qConflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	// !resolved → GetConflicts empty → skip.
	mock.ExpectQuery(qConflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	// TryRepairFKCascadeViolations: an unknown violating table → hadViol=true,
	// repaired=false (bd can't auto-repair it).
	mock.ExpectQuery(qViolations).
		WillReturnRows(sqlmock.NewRows([]string{"table"}).AddRow("mystery_table"))
	// hadViol && !repaired → abortMerge, then the "cannot auto-repair" error.
	mock.ExpectExec(qAbort).WillReturnResult(sqlmock.NewResult(0, 0))

	err := SettleMerge(context.Background(), db, nil, true)
	if err == nil || !strings.Contains(err.Error(), "constraint violations bd cannot auto-repair") {
		t.Fatalf("expected unrepairable-violation error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSettleMerge_ViolationQueryErrorAborts(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	mergeErr := errors.New("original merge failure")
	mock.ExpectQuery(qConflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	mock.ExpectQuery(qConflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	// TryRepairFKCascadeViolations errors on the violations query.
	mock.ExpectQuery(qViolations).WillReturnError(errors.New("violations query boom"))
	// violErr → abortMerge, and with a mergeErr set that error wins.
	mock.ExpectExec(qAbort).WillReturnResult(sqlmock.NewResult(0, 0))

	err := SettleMerge(context.Background(), db, mergeErr, true)
	if !errors.Is(err, mergeErr) {
		t.Fatalf("expected the merge error surfaced over the violation error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestSettleMerge_MergeErrNonConflictAborts(t *testing.T) {
	t.Parallel()
	db, mock := newConflictMock(t)
	mergeErr := errors.New("network reset mid-merge")
	// No conflicts to resolve.
	mock.ExpectQuery(qConflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	// !resolved → GetConflicts empty → skip conflict-error path.
	mock.ExpectQuery(qConflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
	// No violations.
	mock.ExpectQuery(qViolations).WillReturnRows(sqlmock.NewRows([]string{"table"}))
	// mergeErr != nil && !resolved && !repaired → abortMerge then return mergeErr.
	mock.ExpectExec(qAbort).WillReturnResult(sqlmock.NewResult(0, 0))

	err := SettleMerge(context.Background(), db, mergeErr, true)
	if !errors.Is(err, mergeErr) {
		t.Fatalf("expected the merge error surfaced, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}
