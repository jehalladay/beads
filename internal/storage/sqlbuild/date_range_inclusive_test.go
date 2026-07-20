package sqlbuild

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-ycoly: date range bounds must be INCLUSIVE (>= / <=), matching the
// priority axis (priority >= ? / priority <= ?) and the documented contract in
// the reversed-range guard (list_input.go / reversed_range.go), which states
// that after==before is a VALID inclusive point range and describes the WHERE
// as "col >= after AND col <= before". With the old strict >/< a value stored
// exactly at a bound (date-only flags parse to midnight) was dropped from BOTH
// --X-after D and --X-before D, and an equal-bounds point query was always
// empty — contradicting the guard's own promise.

// TestBuildIssueFilterClauses_DateRangeInclusive pins the bd list / count /
// search path (BuildIssueFilterClauses, filter.go date loop).
func TestBuildIssueFilterClauses_DateRangeInclusive(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	f := types.IssueFilter{
		CreatedAfter:  &now,
		CreatedBefore: &now,
		UpdatedAfter:  &now,
		UpdatedBefore: &now,
		ClosedAfter:   &now,
		ClosedBefore:  &now,
		StartedAfter:  &now,
		StartedBefore: &now,
		DeferAfter:    &now,
		DeferBefore:   &now,
		DueAfter:      &now,
		DueBefore:     &now,
	}
	where, _, err := BuildIssueFilterClauses("", f, IssuesFilterTables)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"created_at >= ?", "created_at <= ?",
		"updated_at >= ?", "updated_at <= ?",
		"closed_at >= ?", "closed_at <= ?",
		"started_at >= ?", "started_at <= ?",
		"defer_until >= ?", "defer_until <= ?",
		"due_at >= ?", "due_at <= ?",
	} {
		if !hasClause(where, want) {
			t.Errorf("missing inclusive clause %q in %v", want, where)
		}
	}
	// A strict bound would silently drop an exact-boundary row; guard against a
	// regression back to strict >/< on any of the range columns.
	for _, banned := range []string{
		"created_at > ?", "created_at < ?",
		"updated_at > ?", "updated_at < ?",
		"closed_at > ?", "closed_at < ?",
		"started_at > ?", "started_at < ?",
		"defer_until > ?", "defer_until < ?",
	} {
		if hasClause(where, banned) {
			t.Errorf("range bound regressed to strict %q in %v", banned, where)
		}
	}
}

// TestBuildReadyWorkWhere_DateRangeInclusive pins the bd ready path
// (BuildReadyWorkWhere, ready.go date loop) — both stacks must stay in parity.
func TestBuildReadyWorkWhere_DateRangeInclusive(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	f := types.WorkFilter{
		CreatedAfter:  &now,
		CreatedBefore: &now,
		UpdatedAfter:  &now,
		UpdatedBefore: &now,
		DueAfter:      &now,
		DueBefore:     &now,
	}
	where, _, err := BuildReadyWorkWhere(f, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"created_at >= ?", "created_at <= ?",
		"updated_at >= ?", "updated_at <= ?",
		"due_at >= ?", "due_at <= ?",
	} {
		if !strings.Contains(where, want) {
			t.Errorf("missing inclusive clause %q in ready WHERE: %q", want, where)
		}
	}
	// The --overdue predicate (due_at IS NOT NULL AND due_at < ?) legitimately
	// stays strict; only the range-bound "due_at < ?" (no NULL guard) is banned.
	for _, banned := range []string{"created_at > ?", "created_at < ?", "updated_at > ?", "updated_at < ?"} {
		if strings.Contains(where, banned) {
			t.Errorf("ready range bound regressed to strict %q in %q", banned, where)
		}
	}
}
