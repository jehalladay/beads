package query

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// evalToFilter parses q and evaluates it against a fixed reference time,
// returning the resulting IssueFilter.
func evalToFilter(t *testing.T, q string) *types.IssueFilter {
	t.Helper()
	node, err := Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", q, err)
	}
	res, err := NewEvaluator(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)).Evaluate(node)
	if err != nil {
		t.Fatalf("Evaluate(%q) error = %v", q, err)
	}
	return &res.Filter
}

func TestApplyCreatedFilterOperatorBranches(t *testing.T) {
	// = brackets the whole day (both After and Before set).
	f := evalToFilter(t, `created="2026-01-15"`)
	if f.CreatedAfter == nil || f.CreatedBefore == nil {
		t.Fatalf("created= should set both bounds: after=%v before=%v", f.CreatedAfter, f.CreatedBefore)
	}
	if !f.CreatedBefore.After(*f.CreatedAfter) {
		t.Errorf("created= before %v should be after %v", f.CreatedBefore, f.CreatedAfter)
	}

	// >= sets CreatedAfter.
	if f := evalToFilter(t, `created>="2026-01-15"`); f.CreatedAfter == nil {
		t.Error("created>= should set CreatedAfter")
	}

	// <= sets CreatedBefore to the end of the day.
	if f := evalToFilter(t, `created<="2026-01-15"`); f.CreatedBefore == nil {
		t.Error("created<= should set CreatedBefore")
	}

	// < sets CreatedBefore.
	if f := evalToFilter(t, `created<"2026-01-15"`); f.CreatedBefore == nil {
		t.Error("created< should set CreatedBefore")
	}
}

func TestApplyUpdatedFilterOperatorBranches(t *testing.T) {
	f := evalToFilter(t, `updated="2026-01-15"`)
	if f.UpdatedAfter == nil || f.UpdatedBefore == nil {
		t.Fatalf("updated= should set both bounds: after=%v before=%v", f.UpdatedAfter, f.UpdatedBefore)
	}
	if f := evalToFilter(t, `updated>="2026-01-15"`); f.UpdatedAfter == nil {
		t.Error("updated>= should set UpdatedAfter")
	}
	if f := evalToFilter(t, `updated<="2026-01-15"`); f.UpdatedBefore == nil {
		t.Error("updated<= should set UpdatedBefore")
	}
}

func TestApplyClosedFilterOperatorBranches(t *testing.T) {
	if f := evalToFilter(t, `closed>="2026-01-15"`); f.ClosedAfter == nil {
		t.Error("closed>= should set ClosedAfter")
	}
	if f := evalToFilter(t, `closed<="2026-01-15"`); f.ClosedBefore == nil {
		t.Error("closed<= should set ClosedBefore")
	}
	if f := evalToFilter(t, `closed<"2026-01-15"`); f.ClosedBefore == nil {
		t.Error("closed< should set ClosedBefore")
	}
}

func TestApplyStartedFilterOperatorBranches(t *testing.T) {
	if f := evalToFilter(t, `started>="2026-01-15"`); f.StartedAfter == nil {
		t.Error("started>= should set StartedAfter")
	}
	if f := evalToFilter(t, `started<="2026-01-15"`); f.StartedBefore == nil {
		t.Error("started<= should set StartedBefore")
	}
	if f := evalToFilter(t, `started<"2026-01-15"`); f.StartedBefore == nil {
		t.Error("started< should set StartedBefore")
	}
}

// closed and started do not support the equality operator; it must error
// through the switch default rather than silently no-op.
func TestClosedAndStartedRejectEqualsOperator(t *testing.T) {
	for _, q := range []string{`closed="2026-01-15"`, `started="2026-01-15"`} {
		node, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", q, err)
		}
		if _, err := NewEvaluator(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)).Evaluate(node); err == nil {
			t.Errorf("Evaluate(%q) should error on unsupported = operator", q)
		}
	}
}
