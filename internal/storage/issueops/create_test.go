package issueops

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/types"
)

func TestValidateCreateIssuesMixedBucketDependenciesRejectsCrossBucketEdges(t *testing.T) {
	regularA := &types.Issue{ID: "test-regular-a", IssueType: types.TypeTask}
	regularB := &types.Issue{ID: "test-regular-b", IssueType: types.TypeTask}
	wispA := &types.Issue{ID: "test-wisp-a", IssueType: types.TypeTask, Ephemeral: true}
	wispB := &types.Issue{ID: "test-wisp-b", IssueType: types.TypeTask, Ephemeral: true}

	tests := []struct {
		name      string
		issues    []*types.Issue
		wantError bool
	}{
		{
			name: "regular to wisp",
			issues: []*types.Issue{
				{
					ID:        regularA.ID,
					IssueType: types.TypeTask,
					Dependencies: []*types.Dependency{{
						DependsOnID: wispA.ID,
						Type:        types.DepBlocks,
					}},
				},
				wispA,
			},
			wantError: true,
		},
		{
			name: "wisp to regular",
			issues: []*types.Issue{
				regularA,
				{
					ID:        wispA.ID,
					IssueType: types.TypeTask,
					Ephemeral: true,
					Dependencies: []*types.Dependency{{
						DependsOnID: regularA.ID,
						Type:        types.DepBlocks,
					}},
				},
			},
			wantError: true,
		},
		{
			name: "same bucket dependencies",
			issues: []*types.Issue{
				regularB,
				{
					ID:        regularA.ID,
					IssueType: types.TypeTask,
					Dependencies: []*types.Dependency{{
						DependsOnID: regularB.ID,
						Type:        types.DepBlocks,
					}},
				},
				wispB,
				{
					ID:        wispA.ID,
					IssueType: types.TypeTask,
					Ephemeral: true,
					Dependencies: []*types.Dependency{{
						DependsOnID: wispB.ID,
						Type:        types.DepBlocks,
					}},
				},
			},
		},
		{
			name: "out of batch target",
			issues: []*types.Issue{
				{
					ID:        regularA.ID,
					IssueType: types.TypeTask,
					Dependencies: []*types.Dependency{{
						DependsOnID: "test-external-wisp",
						Type:        types.DepBlocks,
					}},
				},
				wispA,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCreateIssuesMixedBucketDependencies(tt.issues)
			if tt.wantError {
				if err == nil || !strings.Contains(err.Error(), "cross-bucket dependency") {
					t.Fatalf("error = %v, want cross-bucket dependency", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

func TestFilterCreateIssuesMixedBucketDependenciesSkipsWhenConfigured(t *testing.T) {
	regular := &types.Issue{
		ID:        "test-regular-source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "test-wisp-target",
			Type:        types.DepBlocks,
		}},
	}
	wisp := &types.Issue{
		ID:        "test-wisp-target",
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	var skipped []string

	filtered, err := filterCreateIssuesMixedBucketDependencies([]*types.Issue{regular, wisp}, storage.BatchCreateOptions{
		SkipDependencyValidationErrors: true,
		OnSkippedDependency: func(issueID, dependsOnID, reason string) {
			skipped = append(skipped, issueID+" -> "+dependsOnID+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("filterCreateIssuesMixedBucketDependencies error = %v, want nil", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("len(filtered) = %d, want 2", len(filtered))
	}
	if len(filtered[0].Dependencies) != 0 {
		t.Fatalf("filtered[0].Dependencies = %#v, want none", filtered[0].Dependencies)
	}
	if len(regular.Dependencies) != 1 {
		t.Fatalf("regular.Dependencies was mutated to %#v, want original dependency preserved", regular.Dependencies)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "test-regular-source -> test-wisp-target") ||
		!strings.Contains(skipped[0], "cross-bucket dependency") {
		t.Fatalf("skipped = %#v, want cross-bucket dependency detail", skipped)
	}
}

func TestPersistDependenciesHonorsImportedCreatedBy(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()

	target := &types.Issue{ID: "target", IssueType: types.TypeTask}
	source := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepRelated,
			CreatedBy:   "someone.else",
		}},
	}

	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("target").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("target").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("INSERT INTO dependencies").
		WithArgs(depid.New("source", "target"), "source", "target", types.DepRelated, "someone.else", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{target, source}, "current.user", storage.BatchCreateOptions{})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}
	if !result.ChangedTables["dependencies"] {
		t.Fatalf("ChangedTables = %#v, want dependencies changed", result.ChangedTables)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPersistDependenciesDefaultsCreatedByToActor(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()

	target := &types.Issue{ID: "target", IssueType: types.TypeTask}
	source := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepRelated,
		}},
	}

	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("target").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("target").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("INSERT INTO dependencies").
		WithArgs(depid.New("source", "target"), "source", "target", types.DepRelated, "current.user", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{target, source}, "current.user", storage.BatchCreateOptions{})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPersistDependenciesReturnsTargetLookupErrors(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	targetErr := errors.New("target lookup failed")
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepBlocks,
		}},
	}

	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("target").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("target").
		WillReturnError(targetErr)

	err := PersistDependencies(ctx, tx, []*types.Issue{issue}, "tester")
	if err == nil || !strings.Contains(err.Error(), "failed to check dependency target target for source") {
		t.Fatalf("error = %v, want dependency target lookup error", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPersistDependenciesSkipsValidationErrorsWhenConfigured(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "source",
			Type:        types.DepBlocks,
		}},
	}
	var skipped []string

	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("source").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("source").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	result, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{issue}, "tester", storage.BatchCreateOptions{
		SkipDependencyValidationErrors: true,
		OnSkippedDependency: func(issueID, dependsOnID, reason string) {
			skipped = append(skipped, issueID+" -> "+dependsOnID+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}
	if len(result.ChangedTables) != 0 {
		t.Fatalf("ChangedTables = %#v, want none", result.ChangedTables)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "source -> source") ||
		!strings.Contains(skipped[0], "cannot depend on itself") {
		t.Fatalf("skipped = %#v, want self-dependency detail", skipped)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPrepareIssueForInsert_InvalidTypeError verifies the invalid-type failure
// surfaces the valid-type hint and, when the issue has no ID yet, labels it by
// title instead of producing a bare "issue :" fragment (beads-4fh).
func TestPrepareIssueForInsert_InvalidTypeError(t *testing.T) {
	issue := &types.Issue{
		Title:     "my new bead",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.IssueType("frobnicate"),
	}
	err := PrepareIssueForInsert(issue, nil, nil)
	if err == nil {
		t.Fatal("expected validation error for invalid type")
	}
	msg := err.Error()
	if !strings.Contains(msg, "frobnicate") {
		t.Errorf("error should name the invalid type; got: %s", msg)
	}
	if !strings.Contains(msg, "task") || !strings.Contains(msg, "bug") {
		t.Errorf("error should list valid types; got: %s", msg)
	}
	// Empty ID -> labelled by quoted title, not a bare colon.
	if strings.Contains(msg, "for issue :") {
		t.Errorf("empty-id issue should not produce bare 'issue :' fragment; got: %s", msg)
	}
	if !strings.Contains(msg, `"my new bead"`) {
		t.Errorf("error should label issue by title when ID is empty; got: %s", msg)
	}
}

// TestPrepareIssueForInsert_Valid confirms a well-formed issue passes and gets a
// content hash computed.
func TestPrepareIssueForInsert_Valid(t *testing.T) {
	issue := &types.Issue{
		ID:        "bd-1",
		Title:     "ok",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := PrepareIssueForInsert(issue, nil, nil); err != nil {
		t.Fatalf("PrepareIssueForInsert: %v", err)
	}
	if issue.ContentHash == "" {
		t.Error("expected ContentHash to be computed")
	}
}

func TestIssueLabel(t *testing.T) {
	if got := issueLabel(&types.Issue{ID: "bd-9"}); got != "bd-9" {
		t.Errorf("with ID -> %q, want bd-9", got)
	}
	if got := issueLabel(&types.Issue{Title: "hello"}); got != `"hello"` {
		t.Errorf("no ID -> %q, want quoted title", got)
	}
	if got := issueLabel(&types.Issue{}); got != "(unnamed)" {
		t.Errorf("empty -> %q, want (unnamed)", got)
	}
}
