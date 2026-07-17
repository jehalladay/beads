package query

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestDateOperatorBounds pins the per-operator time-bound contract for date
// filters (beads-76y9). Before the fix, <, >, and >= used the raw parsed
// instant while = and <= day-snapped, so the operators disagreed on the same
// input and > was indistinguishable from >=. The contract (option B — snap
// every operator to the input's day, matching the documented =/<= behavior):
//
//	<  DATE -> Before = dayStart(DATE)        (strictly before the day)
//	<= DATE -> Before = dayEnd(DATE)          (through the end of the day)
//	=  DATE -> [dayStart(DATE), dayEnd(DATE))  (the whole day)
//	>  DATE -> After  = dayEnd(DATE)          (strictly after the day)
//	>= DATE -> After  = dayStart(DATE)        (the day onward)
//
// where dayStart = 00:00:00.000000000 and dayEnd = the next day's 00:00:00
// (the exclusive upper bound; SQL applies created_at < dayEnd).
func TestDateOperatorBounds(t *testing.T) {
	// A fixed reference date so any duration/relative parsing is deterministic.
	ref := time.Date(2026, 6, 1, 12, 0, 0, 0, time.Local)
	const date = "2026-01-15"
	dayStart := time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.Add(24 * time.Hour)

	eval := func(t *testing.T, q string) *types.IssueFilter {
		t.Helper()
		node, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q): %v", q, err)
		}
		res, err := NewEvaluator(ref).Evaluate(node)
		if err != nil {
			t.Fatalf("Evaluate(%q): %v", q, err)
		}
		return &res.Filter
	}

	eq := func(t *testing.T, got *time.Time, want time.Time, label string) {
		t.Helper()
		if got == nil {
			t.Fatalf("%s = nil, want %s", label, want)
		}
		if !got.Equal(want) {
			t.Errorf("%s = %s, want %s", label, got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
		}
	}
	nilB := func(t *testing.T, got *time.Time, label string) {
		t.Helper()
		if got != nil {
			t.Errorf("%s = %s, want nil", label, got.Format(time.RFC3339Nano))
		}
	}

	// field is the query field; get returns (after, before) for the field's filter.
	fields := []struct {
		name string
		get  func(f *types.IssueFilter) (after, before *time.Time)
	}{
		{"created", func(f *types.IssueFilter) (*time.Time, *time.Time) { return f.CreatedAfter, f.CreatedBefore }},
		{"updated", func(f *types.IssueFilter) (*time.Time, *time.Time) { return f.UpdatedAfter, f.UpdatedBefore }},
		{"closed", func(f *types.IssueFilter) (*time.Time, *time.Time) { return f.ClosedAfter, f.ClosedBefore }},
		{"started", func(f *types.IssueFilter) (*time.Time, *time.Time) { return f.StartedAfter, f.StartedBefore }},
	}

	for _, fd := range fields {
		fd := fd
		t.Run(fd.name, func(t *testing.T) {
			// <  -> Before = dayStart, After nil
			a, b := fd.get(eval(t, fd.name+`<"`+date+`"`))
			nilB(t, a, fd.name+"< After")
			eq(t, b, dayStart, fd.name+"< Before")

			// <= -> Before = dayEnd, After nil
			a, b = fd.get(eval(t, fd.name+`<="`+date+`"`))
			nilB(t, a, fd.name+"<= After")
			eq(t, b, dayEnd, fd.name+"<= Before")

			// >  -> After = dayEnd, Before nil
			a, b = fd.get(eval(t, fd.name+`>"`+date+`"`))
			eq(t, a, dayEnd, fd.name+"> After")
			nilB(t, b, fd.name+"> Before")

			// >= -> After = dayStart, Before nil
			a, b = fd.get(eval(t, fd.name+`>="`+date+`"`))
			eq(t, a, dayStart, fd.name+">= After")
			nilB(t, b, fd.name+">= Before")

			// > and >= must now differ (the collapse bug).
			gtA, _ := fd.get(eval(t, fd.name+`>"`+date+`"`))
			geA, _ := fd.get(eval(t, fd.name+`>="`+date+`"`))
			if gtA != nil && geA != nil && gtA.Equal(*geA) {
				t.Errorf("%s > and >= produced identical After bound %s (collapse bug)", fd.name, gtA)
			}
		})
	}

	// = is only supported by created/updated (they set both bounds to bracket
	// the day); closed/started have no = branch. Assert the bracket for created.
	t.Run("created= brackets the whole day", func(t *testing.T) {
		f := eval(t, `created="`+date+`"`)
		eq(t, f.CreatedAfter, dayStart, "created= After")
		eq(t, f.CreatedBefore, dayEnd, "created= Before")
	})
}
