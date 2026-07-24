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
		WithArgs(depid.New("source", "target"), "source", "target", types.DepRelated, "someone.else", sqlmock.AnyArg(), "{}", "").
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
		WithArgs(depid.New("source", "target"), "source", "target", types.DepRelated, "current.user", sqlmock.AnyArg(), "{}", "").
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

// TestPersistDependenciesSurfacesCrossKindIDCollision is the beads-xaxe
// containment: depid.New keys on the flattened (issue_id, target-string) with no
// target-kind marker, so an issue-target and a wisp-target sharing the same id
// string collide on one PK. When the batch INSERT's ON DUPLICATE KEY UPDATE
// no-ops (rowsAffected=0) because a row with a DIFFERENT typed target column
// already occupies that PK, the edge must be surfaced as skipped, not silently
// collapsed.
func TestPersistDependenciesSurfacesCrossKindIDCollision(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()

	// "X" is an issue-target here; a wisp-target "X" (same string) is assumed to
	// already occupy depid.New("source","X") in the depends_on_wisp_id column.
	source := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "X",
			Type:        types.DepRelated,
		}},
	}
	var skipped []string

	// ClassifyDepTarget: not a wisp → issue kind.
	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("X").
		WillReturnError(sql.ErrNoRows)
	// Issue-existence check for the target.
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("X").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	// INSERT ... ON DUPLICATE KEY UPDATE no-ops (rowsAffected=0): the PK is
	// already taken by the colliding wisp-target edge.
	mock.ExpectExec("INSERT INTO dependencies").
		WithArgs(depid.New("source", "X"), "source", "X", types.DepRelated, "tester", sqlmock.AnyArg(), "{}", "").
		WillReturnResult(sqlmock.NewResult(0, 0))
	// Collision probe: no row at this PK with THIS kind's column (issue) → the
	// occupant is a different target kind → cross-kind collision.
	mock.ExpectQuery("SELECT 1 FROM dependencies WHERE id = \\? AND depends_on_issue_id = \\?").
		WithArgs(depid.New("source", "X"), "X").
		WillReturnError(sql.ErrNoRows)

	result, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{source}, "tester", storage.BatchCreateOptions{
		OnSkippedDependency: func(issueID, dependsOnID, reason string) {
			skipped = append(skipped, issueID+" -> "+dependsOnID+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}
	// The colliding edge must NOT be counted as a successful change...
	if result.ChangedTables["dependencies"] {
		t.Errorf("ChangedTables marked dependencies changed, but the edge collided and was skipped")
	}
	// ...and it MUST be surfaced as skipped, not silently dropped.
	if len(skipped) != 1 || !strings.Contains(skipped[0], "source -> X") ||
		!strings.Contains(skipped[0], "collision") {
		t.Fatalf("skipped = %#v, want a cross-kind id-collision skip for source -> X", skipped)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesSameKindReimportIsCleanNoOp verifies the other
// rowsAffected=0 branch (beads-xaxe): a same-kind idempotent re-import (the edge
// already exists in THIS kind's column) is a legitimate no-op — NOT a collision,
// so it must not be surfaced as skipped.
func TestPersistDependenciesSameKindReimportIsCleanNoOp(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()

	source := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepRelated,
		}},
	}
	var skipped []string

	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("target").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("target").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("INSERT INTO dependencies").
		WithArgs(depid.New("source", "target"), "source", "target", types.DepRelated, "tester", sqlmock.AnyArg(), "{}", "").
		WillReturnResult(sqlmock.NewResult(0, 0))
	// Probe finds the row in THIS kind's column → clean same-kind re-import.
	mock.ExpectQuery("SELECT 1 FROM dependencies WHERE id = \\? AND depends_on_issue_id = \\?").
		WithArgs(depid.New("source", "target"), "target").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	_, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{source}, "tester", storage.BatchCreateOptions{
		OnSkippedDependency: func(issueID, dependsOnID, reason string) {
			skipped = append(skipped, issueID+" -> "+dependsOnID+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %#v, want none (same-kind re-import is a clean no-op)", skipped)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesPreservesCrossPrefixTarget verifies that an imported
// dependency edge whose target lives in another rig's database (a bare
// cross-prefix ID like "other-999", NOT the "external:" form) is preserved,
// not dropped. Every interactive path (dep add / link) computes
// IsCrossPrefix = ExtractPrefix(source) != ExtractPrefix(target) and treats
// such a target as external, skipping the local-existence check. Import once
// hardcoded ClassifyDepTarget(..., false), so a cross-prefix target was
// validated against the LOCAL issues table, missed, and was silently skipped
// as "target not found" — losing a legitimate edge that dep add accepts and
// export emits (beads-77i6, lossy export->import round-trip). The import path
// must derive cross-prefix the same way. A cross-prefix target must therefore
// issue NO wisps/issues existence query and INSERT into depends_on_external.
func TestPersistDependenciesPreservesCrossPrefixTarget(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()

	source := &types.Issue{
		ID:        "foo-1",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "other-999", // different prefix -> lives in another rig's DB
			Type:        types.DepRelated,
		}},
	}

	// No wisps/issues existence query: a cross-prefix target is external, so the
	// local-existence check must be skipped (exactly as the interactive paths do).
	mock.ExpectExec("INSERT INTO dependencies").
		WithArgs(depid.New("foo-1", "other-999"), "foo-1", "other-999", types.DepRelated, "tester", sqlmock.AnyArg(), "{}", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	var skipped []string
	result, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{source}, "tester", storage.BatchCreateOptions{
		SkipDependencyValidationErrors: true,
		OnSkippedDependency: func(issueID, dependsOnID, reason string) {
			skipped = append(skipped, issueID+" -> "+dependsOnID+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %#v, want none — a cross-prefix edge must be preserved, not dropped", skipped)
	}
	if !result.ChangedTables["dependencies"] {
		t.Fatalf("ChangedTables = %#v, want dependencies changed (cross-prefix edge inserted)", result.ChangedTables)
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

// TestPersistDependenciesReportsEmptyTargetWithActionableReason verifies that a
// dependency edge whose depends_on_id is empty (e.g. hand-authored JSONL that
// used the top-level "id" field instead of "depends_on_id") is skipped with a
// reason that names the missing field, rather than the misleading empty
// "source -> : target not found" (beads-p96v). The guard must short-circuit
// BEFORE any target lookup, so no SQL is issued for the empty-target edge.
func TestPersistDependenciesReportsEmptyTargetWithActionableReason(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "", // missing depends_on_id
			Type:        types.DepBlocks,
		}},
	}
	var skipped []string

	// No mock.ExpectQuery: the empty-target guard must fire before any lookup.

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
	if len(skipped) != 1 {
		t.Fatalf("skipped = %#v, want exactly one skipped edge", skipped)
	}
	if !strings.Contains(skipped[0], "depends_on_id") {
		t.Errorf("skipped reason should name the missing depends_on_id field; got: %s", skipped[0])
	}
	if strings.Contains(skipped[0], "target not found") {
		t.Errorf("empty-target skip should NOT reuse the misleading 'target not found' reason; got: %s", skipped[0])
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesSkipsEmptyTypeWithActionableReason is the beads-3rk4
// containment: an imported edge with an empty "type" must not persist. Every
// interactive dep-creation path enforces DependencyType.IsValid() (non-empty,
// <=32 chars); import bypassed it, so an empty-type edge slipped in — and
// because the cycle guard only fires for blocking types, an empty-type 2-cycle
// survived import that dep add rejects in every direction. Under
// SkipDependencyValidationErrors the empty-type edge must be skipped with a
// reason naming the missing "type" field, before any target lookup.
func TestPersistDependenciesSkipsEmptyTypeWithActionableReason(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        "", // missing type
		}},
	}
	var skipped []string

	// No mock.ExpectQuery: the empty-type guard must fire before any lookup.

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
	if len(skipped) != 1 {
		t.Fatalf("skipped = %#v, want exactly one skipped edge", skipped)
	}
	if !strings.Contains(skipped[0], "type") {
		t.Errorf("skipped reason should name the invalid dependency type field; got: %s", skipped[0])
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesErrorsOnEmptyTypeWhenNotSkipping asserts the strict
// (non-import-tolerant) contract: without SkipDependencyValidationErrors an
// empty-type edge is a hard error, mirroring dep add / link / create (beads-3rk4).
func TestPersistDependenciesErrorsOnEmptyTypeWhenNotSkipping(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        "", // missing type
		}},
	}

	// No mock.ExpectQuery: the empty-type guard must fire before any lookup.

	_, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{issue}, "tester", storage.BatchCreateOptions{})
	if err == nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = nil, want error for empty dependency type")
	}
	if !strings.Contains(err.Error(), "type") {
		t.Errorf("error should name the invalid dependency type field; got: %v", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesSkipsMalformedJSONMetadataWithActionableReason is the
// beads-u47yy teeth: dep.Metadata is persisted verbatim into a Dolt JSON column,
// so a malformed-JSON payload (e.g. a truncated `{"gate":"any-children"`) hit the
// column and returned Dolt Error 1105, which aborts the whole ExecContext and
// rolls back the batch — ZERO issues import despite only one bad edge. Under
// SkipDependencyValidationErrors the malformed edge must skip-with-reason before
// the INSERT is ever reached. Mutation-verify: delete the json.Valid guard and
// this expectation goes unmet (the guard is load-bearing, not decorative).
func TestPersistDependenciesSkipsMalformedJSONMetadataWithActionableReason(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepRelated,
			Metadata:    `{"gate":"any-children"`, // truncated: not well-formed JSON
		}},
	}
	var skipped []string

	// No mock.ExpectQuery: the metadata guard must fire before any lookup/INSERT.

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
	if len(skipped) != 1 {
		t.Fatalf("skipped = %#v, want exactly one skipped edge", skipped)
	}
	if !strings.Contains(skipped[0], "metadata") || !strings.Contains(skipped[0], "JSON") {
		t.Errorf("skipped reason should name the malformed metadata / JSON; got: %s", skipped[0])
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesSkipsUnknownTypeWithActionableReason is the beads-p4pvr
// containment (dep-type leg): every command-layer entry point (dep add,
// create --deps, link, bd batch — the last added by beads-cqk1) rejects an
// unknown/non-well-known dependency type via DependencyType.IsWellKnown(), but
// import only checked IsValid() (non-empty, <=32 chars). So a crafted/drifted
// import row with type "bogus_type" landed as a PHANTOM edge: dep list DISPLAYS
// it, but dependency_count=0 and readiness ignores it (no query recognizes the
// type). Under SkipDependencyValidationErrors the unknown-type edge must be
// skipped with a reason naming the type, before any target lookup — mirroring
// the empty-type guard (beads-3rk4).
func TestPersistDependenciesSkipsUnknownTypeWithActionableReason(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        "bogus_type", // IsValid() true (non-empty, <=32) but not well-known
		}},
	}
	var skipped []string

	// No mock.ExpectQuery: the unknown-type guard must fire before any lookup.

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
	if len(skipped) != 1 {
		t.Fatalf("skipped = %#v, want exactly one skipped edge", skipped)
	}
	if !strings.Contains(skipped[0], "bogus_type") {
		t.Errorf("skipped reason should name the unknown dependency type; got: %s", skipped[0])
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesErrorsOnMalformedJSONMetadataWhenNotSkipping asserts the
// strict (non-import-tolerant) contract: without SkipDependencyValidationErrors a
// malformed-metadata edge is a hard error naming the edge, mirroring the empty-
// type / bad-gate guards (beads-u47yy).
func TestPersistDependenciesErrorsOnMalformedJSONMetadataWhenNotSkipping(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepRelated,
			Metadata:    `{"gate":"any-children"`, // truncated: not well-formed JSON
		}},
	}

	// No mock.ExpectQuery: the metadata guard must fire before any lookup/INSERT.

	_, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{issue}, "tester", storage.BatchCreateOptions{})
	if err == nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = nil, want error for malformed metadata")
	}
	if !strings.Contains(err.Error(), "metadata") || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error should name the malformed metadata / JSON; got: %v", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesErrorsOnUnknownTypeWhenNotSkipping asserts the strict
// (non-import-tolerant) contract: without SkipDependencyValidationErrors an
// unknown-type edge is a hard error, mirroring dep add / link / create / batch
// which all gate on IsWellKnown() (beads-p4pvr).
func TestPersistDependenciesErrorsOnUnknownTypeWhenNotSkipping(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        "bogus_type",
		}},
	}

	// No mock.ExpectQuery: the unknown-type guard must fire before any lookup.

	_, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{issue}, "tester", storage.BatchCreateOptions{})
	if err == nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = nil, want error for unknown dependency type")
	}
	if !strings.Contains(err.Error(), "bogus_type") {
		t.Errorf("error should name the unknown dependency type; got: %v", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesMalformedMetadataDoesNotAbortValidEdges is the core
// regression for beads-u47yy: a single malformed-metadata edge must NOT abort the
// batch — subsequent VALID edges in the same call still get persisted. Before the
// guard, the bad edge's INSERT raised Dolt Error 1105 and rolled back everything.
// The bad edge (first) skips-with-reason; the good edge (second) runs the full
// lookup + INSERT and marks the dependencies table changed.
func TestPersistDependenciesMalformedMetadataDoesNotAbortValidEdges(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{
			{
				DependsOnID: "badtarget",
				Type:        types.DepRelated,
				Metadata:    `{not valid json`, // malformed: skipped, must not abort
			},
			{
				DependsOnID: "goodtarget",
				Type:        types.DepRelated,
				Metadata:    `{"gate":"any-children"}`, // valid: must still land
			},
		},
	}
	var skipped []string

	// Only the GOOD edge reaches the lookup + INSERT; the bad edge is guarded out.
	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("goodtarget").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("goodtarget").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("INSERT INTO dependencies").
		WithArgs(depid.New("source", "goodtarget"), "source", "goodtarget", types.DepRelated, "tester", sqlmock.AnyArg(), `{"gate":"any-children"}`, "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{issue}, "tester", storage.BatchCreateOptions{
		SkipDependencyValidationErrors: true,
		OnSkippedDependency: func(issueID, dependsOnID, reason string) {
			skipped = append(skipped, issueID+" -> "+dependsOnID+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %#v, want exactly one skipped edge (the malformed one)", skipped)
	}
	if !strings.Contains(skipped[0], "badtarget") {
		t.Errorf("the skipped edge should be the malformed badtarget one; got: %s", skipped[0])
	}
	if len(result.ChangedTables) == 0 {
		t.Fatalf("ChangedTables = %#v, want the valid edge to have marked dependencies changed", result.ChangedTables)
	}

	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesSkipsBogusWaitsForGateWithActionableReason is the
// beads-p4pvr second family member (waits-for gate leg): the command layer
// (create_deps.go buildWaitsFor, create.go) rejects a --waits-for-gate value
// that is not all-children/any-children, but import stored the gate value
// VERBATIM from dep metadata. A waits-for edge with {"gate":"bogus-gate-value"}
// sails through import; on read ParseWaitsForGateMetadata silently coerces the
// unrecognized gate to all-children — a silent semantic flip. Under
// SkipDependencyValidationErrors the bogus-gate edge must be skipped with a
// reason naming the gate, before any target lookup.
func TestPersistDependenciesSkipsBogusWaitsForGateWithActionableReason(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	issue := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepWaitsFor,
			Metadata:    `{"gate":"bogus-gate-value"}`,
		}},
	}
	var skipped []string

	// No mock.ExpectQuery: the bogus-gate guard must fire before any lookup.

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
	if len(skipped) != 1 {
		t.Fatalf("skipped = %#v, want exactly one skipped edge", skipped)
	}
	if !strings.Contains(skipped[0], "bogus-gate-value") {
		t.Errorf("skipped reason should name the invalid waits-for gate; got: %s", skipped[0])
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesAcceptsValidWaitsForGate guards against over-rejection:
// a waits-for edge carrying a valid gate (any-children) — or a non-waits-for
// edge whose metadata happens to hold a "gate" key — must NOT be skipped. Only
// waits-for edges with an unrecognized gate value are rejected (beads-p4pvr).
func TestPersistDependenciesAcceptsValidWaitsForGate(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()

	target := &types.Issue{ID: "target", IssueType: types.TypeTask}
	source := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepWaitsFor,
			Metadata:    `{"gate":"any-children"}`,
		}},
	}
	var skipped []string

	// Target existence lookup + the dependency INSERT must both proceed — the
	// valid gate must not short-circuit the edge. Mirrors the gnopw teeth's
	// query sequence (wisps check → issues check → INSERT).
	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("target").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("target").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec("INSERT INTO dependencies").
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{target, source}, "tester", storage.BatchCreateOptions{
		SkipDependencyValidationErrors: true,
		OnSkippedDependency: func(issueID, dependsOnID, reason string) {
			skipped = append(skipped, issueID+" -> "+dependsOnID+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %#v, want no skipped edge for a valid any-children gate", skipped)
	}
	if len(result.ChangedTables) == 0 {
		t.Fatalf("ChangedTables = %#v, want the dependency edge persisted", result.ChangedTables)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestPersistDependenciesPersistsMetadataAndThreadID is the beads-gnopw teeth:
// the batch create/import INSERT must write the edge's metadata and thread_id
// columns (it historically listed neither, so an export->import round-trip
// silently dropped them — e.g. an any-children waits-for gate re-imported as {}
// flips to all-children). A non-empty metadata payload and a thread_id must be
// bound through to the INSERT verbatim. Mutation-verify: drop the two columns
// from the create.go INSERT and this expectation goes unmet.
func TestPersistDependenciesPersistsMetadataAndThreadID(t *testing.T) {
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
			Metadata:    `{"gate": "any-children"}`,
			ThreadID:    "thread-42",
		}},
	}

	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("target").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("target").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	// The last two bound args are the edge's own metadata and thread_id — not
	// the "{}"/"" defaults — proving the payload is carried through, not dropped.
	mock.ExpectExec("INSERT INTO dependencies").
		WithArgs(depid.New("source", "target"), "source", "target", types.DepRelated, "tester", sqlmock.AnyArg(), `{"gate": "any-children"}`, "thread-42").
		WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{target, source}, "tester", storage.BatchCreateOptions{})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (metadata/thread_id not bound to INSERT?): %v", err)
	}
}

// TestPersistDependenciesReimportDoesNotRefreshMetadata locks beads-8292k
// (RULED (A) additive-only): re-importing an edge that already exists at this
// deterministic PK must NOT refresh its stored metadata — first-write-wins.
// The batch import INSERT carries the (changed) metadata into VALUES but the
// `ON DUPLICATE KEY UPDATE type = type` no-op discards it on PK conflict, so
// the stored payload stays authoritative. This deliberately diverges from the
// interactive/domain-db paths (which blind-refresh on same-type re-add) so a
// merge-safe clone round-trip (#4259) is deterministic and cannot silently
// mutate an existing edge, and so the xaxe cross-kind collision stays
// detectable via the rowsAffected==0 probe.
//
// The regression teeth: the ExpectExec regexp anchors the ODKU clause to
// `type = type` at end-of-statement. Switching to a metadata-refreshing form
// (option (B): `ON DUPLICATE KEY UPDATE metadata = VALUES(metadata)`, or
// appending `, metadata = VALUES(metadata)`) no longer matches → the mock
// expectation goes unmet → this test fails, catching an accidental (or
// deliberate-but-unratified) flip away from additive-only.
func TestPersistDependenciesReimportDoesNotRefreshMetadata(t *testing.T) {
	ctx := context.Background()
	db, mock, tx := beginMockTx(t)
	defer db.Close()

	target := &types.Issue{ID: "target", IssueType: types.TypeTask}
	// A re-import of an existing edge whose source metadata has CHANGED (e.g.
	// the waits-for gate flipped any-children -> all-children upstream). The
	// edge already exists at this PK, so the INSERT no-ops.
	source := &types.Issue{
		ID:        "source",
		IssueType: types.TypeTask,
		Dependencies: []*types.Dependency{{
			DependsOnID: "target",
			Type:        types.DepRelated,
			Metadata:    `{"gate": "all-children"}`,
			ThreadID:    "thread-new",
		}},
	}

	mock.ExpectQuery("SELECT 1 FROM wisps WHERE id = \\? LIMIT 1").
		WithArgs("target").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT 1 FROM issues WHERE id = \\?").
		WithArgs("target").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	// The new metadata/thread_id ARE bound into the INSERT's VALUES (the code
	// always carries the payload; the additive-only contract lives in the ODKU
	// clause, not in dropping the columns — that was gnopw's bug). The clause
	// must remain `type = type` anchored to end-of-statement: a metadata-
	// refreshing form (option (B)) would fail to match this regexp.
	mock.ExpectExec(`ON DUPLICATE KEY UPDATE type = type\s*$`).
		WithArgs(depid.New("source", "target"), "source", "target", types.DepRelated, "tester", sqlmock.AnyArg(), `{"gate": "all-children"}`, "thread-new").
		WillReturnResult(sqlmock.NewResult(0, 0)) // rowsAffected==0: PK exists, ODKU no-op'd (no refresh).
	// rowsAffected==0 -> the same-kind probe confirms the row is a legitimate
	// idempotent re-import (present in THIS kind's column), so it is a clean
	// no-op, not a cross-kind collision skip.
	mock.ExpectQuery("SELECT 1 FROM dependencies WHERE id = \\? AND depends_on_issue_id = \\?").
		WithArgs(depid.New("source", "target"), "target").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	var skipped []string
	result, err := PersistDependenciesWithOptionsResult(ctx, tx, []*types.Issue{target, source}, "tester", storage.BatchCreateOptions{
		OnSkippedDependency: func(issueID, dependsOnID, reason string) {
			skipped = append(skipped, issueID+" -> "+dependsOnID+": "+reason)
		},
	})
	if err != nil {
		t.Fatalf("PersistDependenciesWithOptionsResult error = %v, want nil", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %#v, want none (a changed-metadata re-import of a same-kind edge is a clean additive-only no-op, not a collision)", skipped)
	}
	// rowsAffected==0 means nothing was written, so the dependencies table must
	// NOT be marked changed — a refresh (option (B)) would report a change here.
	if result.ChangedTables["dependencies"] {
		t.Fatalf("ChangedTables[dependencies]=true, want false: an additive-only re-import that no-ops must not report a metadata change")
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (import stopped being additive-only — ODKU clause changed away from `type = type`?): %v", err)
	}
}
