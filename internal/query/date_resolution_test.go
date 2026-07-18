package query

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestDateResolutionAwareBounds is the acceptance gate for beads-125q: the
// ordered date operators (<, <=, >, >=) are RESOLUTION-AWARE.
//
//   - TokenDuration (6h, 7d, 2w)  -> PRECISE instant. Day-snapping a duration is
//     nonsensical for sub-day (6h collapses to a whole day) and surprising for
//     multi-day (7d most naturally means the precise now-7d instant).
//   - date-only literal (2026-01-15) -> DAY-BRACKET. Matches GitHub/Jira/Linear
//     whole-day semantics; kept exactly as beads-76y9 shipped it.
//   - explicit timestamp (2026-01-15T10:30:00Z) -> PRECISE instant.
//
// PRECISE bounds map inclusive/exclusive onto the SQL strict >/< comparators
// via a 1ns epsilon (exact in the in-memory predicate leg):
//
//	<  t -> Before = t                 (strictly before the instant)
//	<= t -> Before = t + 1ns           (at or before the instant)
//	>  t -> After  = t                 (strictly after the instant)
//	>= t -> After  = t - 1ns           (at or after the instant)
//
// BRACKET bounds (date literals) are unchanged from beads-76y9:
//
//	<  DATE -> Before = dayStart(DATE)
//	<= DATE -> Before = dayEnd(DATE)
//	>  DATE -> After  = dayEnd(DATE)
//	>= DATE -> After  = dayStart(DATE)
func TestDateResolutionAwareBounds(t *testing.T) {
	// Fixed reference so duration parsing is deterministic; a non-midnight,
	// sub-second time makes precise-vs-bracket differences observable.
	ref := time.Date(2026, 6, 1, 12, 34, 56, 789, time.Local)

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
			t.Fatalf("%s = nil, want %s", label, want.Format(time.RFC3339Nano))
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

	// Precise instants the code computes for each duration/timestamp value.
	subDay := ref.Add(-6 * time.Hour)          // "6h"  -> now - 6h
	multiDay := ref.AddDate(0, 0, -7)          // "7d"  -> now - 7d
	tsStr := "2026-01-15T10:30:00Z"            // explicit timestamp -> precise
	tsInstant, perr := time.Parse(time.RFC3339, tsStr)
	if perr != nil {
		t.Fatalf("bad test timestamp: %v", perr)
	}
	const dateLit = "2026-01-15" // date-only literal -> bracket
	dayStart := time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.Add(24 * time.Hour)

	ns := time.Nanosecond

	// precise asserts the four ordered operators against a precise instant t,
	// on the created field (which supports all ordered ops + =).
	precise := func(t *testing.T, label, val string, inst time.Time) {
		t.Run(label, func(t *testing.T) {
			// <  -> Before = t
			f := eval(t, `created<`+val)
			nilB(t, f.CreatedAfter, label+"< After")
			eq(t, f.CreatedBefore, inst, label+"< Before")
			// <= -> Before = t + 1ns
			f = eval(t, `created<=`+val)
			nilB(t, f.CreatedAfter, label+"<= After")
			eq(t, f.CreatedBefore, inst.Add(ns), label+"<= Before")
			// >  -> After = t
			f = eval(t, `created>`+val)
			eq(t, f.CreatedAfter, inst, label+"> After")
			nilB(t, f.CreatedBefore, label+"> Before")
			// >= -> After = t - 1ns
			f = eval(t, `created>=`+val)
			eq(t, f.CreatedAfter, inst.Add(-ns), label+">= After")
			nilB(t, f.CreatedBefore, label+">= Before")
			// > and >= must differ (76y9 defect #2 must not reappear).
			gt := eval(t, `created>`+val).CreatedAfter
			ge := eval(t, `created>=`+val).CreatedAfter
			if gt != nil && ge != nil && gt.Equal(*ge) {
				t.Errorf("%s: > and >= collapsed to identical After bound %s", label, gt)
			}
			// < and <= must differ.
			lt := eval(t, `created<`+val).CreatedBefore
			le := eval(t, `created<=`+val).CreatedBefore
			if lt != nil && le != nil && lt.Equal(*le) {
				t.Errorf("%s: < and <= collapsed to identical Before bound %s", label, lt)
			}
		})
	}

	precise(t, "subDayDuration(6h)", "6h", subDay)
	precise(t, "multiDayDuration(7d)", "7d", multiDay)
	precise(t, "timestamp", `"`+tsStr+`"`, tsInstant)

	// Date-only literal keeps the day-bracket contract (unchanged from 76y9).
	t.Run("dateLiteral", func(t *testing.T) {
		f := eval(t, `created<"`+dateLit+`"`)
		nilB(t, f.CreatedAfter, "lit< After")
		eq(t, f.CreatedBefore, dayStart, "lit< Before")

		f = eval(t, `created<="`+dateLit+`"`)
		nilB(t, f.CreatedAfter, "lit<= After")
		eq(t, f.CreatedBefore, dayEnd, "lit<= Before")

		f = eval(t, `created>"`+dateLit+`"`)
		eq(t, f.CreatedAfter, dayEnd, "lit> After")
		nilB(t, f.CreatedBefore, "lit> Before")

		f = eval(t, `created>="`+dateLit+`"`)
		eq(t, f.CreatedAfter, dayStart, "lit>= After")
		nilB(t, f.CreatedBefore, "lit>= Before")
	})

	// = / != stay DAY-based for ALL value types (an exact-instant equality is
	// measure-zero — matches nothing — so equality is inherently a day bucket;
	// this preserves the landed `created=5d` -> same-day contract). Only the
	// ordered operators were ever inconsistent (the 76y9 bug); = was not.
	t.Run("equalsStaysDayBracketedForDuration", func(t *testing.T) {
		f := eval(t, `created=7d`)
		if f.CreatedAfter == nil || f.CreatedBefore == nil {
			t.Fatalf("created=7d must day-bracket (both bounds set): after=%v before=%v", f.CreatedAfter, f.CreatedBefore)
		}
		// The bracket must be a whole day (dayEnd - dayStart == 24h), not the
		// precise instant.
		if d := f.CreatedBefore.Sub(*f.CreatedAfter); d != 24*time.Hour {
			t.Errorf("created=7d bracket width = %s, want 24h (whole day)", d)
		}
	})

	// FILTER↔PREDICATE parity for the precise path (extends the beads-7a4t
	// contract to durations): an OR forces the predicate leg; the SAME query
	// must classify a probe instant identically to the SQL-filter bounds. Probe
	// at the exact multiDay instant ± epsilon so the strict >/< boundary is
	// observable.
	t.Run("filterPredicateParityPrecise", func(t *testing.T) {
		probes := []time.Time{
			multiDay.Add(-time.Hour),
			multiDay,
			multiDay.Add(time.Hour),
		}
		for _, op := range []string{"<", "<=", ">", ">="} {
			op := op
			// Filter leg bounds.
			ff := eval(t, `created`+op+`7d`)
			// Predicate leg (OR forces it; id=zzz never matches).
			res, err := NewEvaluator(ref).Evaluate(mustParse(t, `created`+op+`7d OR id=zzz`))
			if err != nil {
				t.Fatalf("op %s: predicate build failed: %v", op, err)
			}
			if res.Predicate == nil {
				t.Fatalf("op %s: expected a predicate (OR should force the predicate leg)", op)
			}
			for _, p := range probes {
				filterMatch := true
				if ff.CreatedAfter != nil && !p.After(*ff.CreatedAfter) {
					filterMatch = false
				}
				if ff.CreatedBefore != nil && !p.Before(*ff.CreatedBefore) {
					filterMatch = false
				}
				predMatch := res.Predicate(&types.Issue{CreatedAt: p})
				if filterMatch != predMatch {
					t.Errorf("op %s probe %s: filter=%v predicate=%v (legs disagree)", op, p.Format(time.RFC3339Nano), filterMatch, predMatch)
				}
			}
		}
	})
}

func mustParse(t *testing.T, q string) Node {
	t.Helper()
	n, err := Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	return n
}
