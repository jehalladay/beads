package query

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestDateOperatorPredicateParity pins beads-7a4t: the ordered date operators
// (< <= > >=) must mean the SAME thing on the PREDICATE leg (compareTime) as on
// the FILTER leg (dateComparisonBounds, fixed by beads-76y9). 76y9 day-snapped
// every operator on the filter leg — < DATE means "strictly before DATE's day",
// > DATE means "strictly after DATE's day", etc. But compareTime still compared
// the raw parsed instant (midnight of DATE) with Before/After, so an issue on
// the SAME day as DATE was classified differently depending on the query shape:
//
//	created>"2026-01-15"  (bare filter)  -> excludes anything on 2026-01-15
//	created>"2026-01-15" OR id=zzz (OR)  -> compareTime: 2026-01-15T18:00 After
//	                                        midnight(2026-01-15) = TRUE (wrong)
//
// The OR / owner= / non-status-!= shapes force the predicate leg, so the same
// query returned a different set by shape. This pins the predicate leg to the
// 76y9 day-granularity contract (=/!= were already same-day and stay).
func TestDateOperatorPredicateParity(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	const date = "2026-01-15"

	// Three probe instants relative to DATE's day (2026-01-15):
	sameDayNoon := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC) // on the day
	dayBefore := time.Date(2026, 1, 14, 23, 0, 0, 0, time.UTC)   // strictly before the day
	dayAfter := time.Date(2026, 1, 16, 1, 0, 0, 0, time.UTC)     // strictly after the day

	// pred builds the predicate leg by forcing an OR (id=zzz never matches, so
	// the created leg alone decides). Returns the predicate for created<op>DATE.
	pred := func(t *testing.T, op string) func(*types.Issue) bool {
		t.Helper()
		q := "created" + op + `"` + date + `" OR id=zzz`
		res, err := EvaluateAt(q, now)
		if err != nil {
			t.Fatalf("EvaluateAt(%q): %v", q, err)
		}
		if res.Predicate == nil {
			t.Fatalf("EvaluateAt(%q): expected a predicate (OR should force the predicate leg)", q)
		}
		return res.Predicate
	}
	match := func(t *testing.T, p func(*types.Issue) bool, ts time.Time) bool {
		t.Helper()
		return p(&types.Issue{CreatedAt: ts})
	}

	t.Run("greater/excludes-same-day", func(t *testing.T) {
		p := pred(t, ">")
		if match(t, p, sameDayNoon) {
			t.Error(`created>"2026-01-15" predicate matched a same-day issue; > must be strictly AFTER the day (raw-instant bug)`)
		}
		if match(t, p, dayBefore) {
			t.Error(`created>DATE matched a day-before issue`)
		}
		if !match(t, p, dayAfter) {
			t.Error(`created>DATE did NOT match a day-after issue`)
		}
	})

	t.Run("greater-eq/includes-same-day", func(t *testing.T) {
		p := pred(t, ">=")
		if !match(t, p, sameDayNoon) {
			t.Error(`created>="2026-01-15" predicate did NOT match a same-day issue; >= starts at the day's start`)
		}
		if match(t, p, dayBefore) {
			t.Error(`created>=DATE matched a day-before issue`)
		}
		if !match(t, p, dayAfter) {
			t.Error(`created>=DATE did NOT match a day-after issue`)
		}
	})

	t.Run("less/excludes-same-day", func(t *testing.T) {
		p := pred(t, "<")
		if match(t, p, sameDayNoon) {
			t.Error(`created<"2026-01-15" predicate matched a same-day issue; < must be strictly BEFORE the day (raw-instant bug)`)
		}
		if !match(t, p, dayBefore) {
			t.Error(`created<DATE did NOT match a day-before issue`)
		}
		if match(t, p, dayAfter) {
			t.Error(`created<DATE matched a day-after issue`)
		}
	})

	t.Run("less-eq/includes-same-day", func(t *testing.T) {
		p := pred(t, "<=")
		if !match(t, p, sameDayNoon) {
			t.Error(`created<="2026-01-15" predicate did NOT match a same-day issue; <= includes through the day's end`)
		}
		if !match(t, p, dayBefore) {
			t.Error(`created<=DATE did NOT match a day-before issue`)
		}
		if match(t, p, dayAfter) {
			t.Error(`created<=DATE matched a day-after issue`)
		}
	})

	// > and >= must now DIFFER on a same-day issue (the collapse the filter-leg
	// test also guards, here proven on the predicate leg).
	t.Run("gt-vs-gte-differ-on-same-day", func(t *testing.T) {
		gt := pred(t, ">")
		gte := pred(t, ">=")
		if match(t, gt, sameDayNoon) == match(t, gte, sameDayNoon) {
			t.Error("> and >= classified a same-day issue identically on the predicate leg (collapse bug)")
		}
	})

	// The bare-filter leg (no OR) must agree with the predicate leg for the same
	// same-day probe — this is the cross-leg parity assertion.
	t.Run("filter-leg-agrees-same-day-greater", func(t *testing.T) {
		// Bare created>DATE sets CreatedAfter = dayEnd(DATE); a same-day noon
		// issue is < dayEnd so the SQL WHERE (created_at > dayEnd) excludes it.
		res, err := EvaluateAt(`created>"`+date+`"`, now)
		if err != nil {
			t.Fatalf("EvaluateAt bare: %v", err)
		}
		if res.Filter.CreatedAfter == nil {
			t.Fatal("bare created>DATE did not set CreatedAfter")
		}
		// SQL is `created_at > CreatedAfter`; a same-day issue at noon satisfies
		// the filter iff noon > CreatedAfter. It must NOT (parity with predicate).
		filterMatches := sameDayNoon.After(*res.Filter.CreatedAfter)
		if filterMatches {
			t.Error("filter leg would MATCH a same-day issue for created>DATE — disagrees with the day-snap contract")
		}
	})
}

// TestNotStatusValidationParity pins beads-lm2z (NOT leg): `NOT status=TYPO`
// must ERROR, mirroring applyStatusFilter's Status.IsValid check and the NOT
// type leg (which already validates via normalizeAndValidateType, beads-123i).
// Before the fix applyNot's status leg did a bare ToLower and appended the bogus
// value to ExcludeStatus, so `NOT status=typo` excluded nothing → matched
// EVERYTHING silently (rc=0). The predicate leg (buildStatusPredicate) is
// already validated by beads-bi4g; this pins the remaining unvalidated NOT leg.
func TestNotStatusValidationParity(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	const bogus = "bogusstatus"

	t.Run("not-status-invalid-errors", func(t *testing.T) {
		_, err := EvaluateAt("NOT status="+bogus, now)
		if err == nil || !strings.Contains(err.Error(), "invalid status") {
			t.Fatalf("EvaluateAt(NOT status=%s) err = %v, want 'invalid status' (not silent match-everything)", bogus, err)
		}
	})

	// A valid status in the NOT leg must still work (no over-rejection).
	t.Run("not-status-valid-ok", func(t *testing.T) {
		res, err := EvaluateAt("NOT status=closed", now)
		if err != nil {
			t.Fatalf("EvaluateAt(NOT status=closed) unexpected err: %v", err)
		}
		found := false
		for _, s := range res.Filter.ExcludeStatus {
			if s == types.StatusClosed {
				found = true
			}
		}
		if !found {
			t.Error("NOT status=closed did not add closed to ExcludeStatus")
		}
	})
}
