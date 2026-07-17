package query

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestNeqRouting pins the != routing contract (beads-pqrn). A bare
// `field!=value` comparison must succeed for every field whose predicate
// supports != — before the fix, canUseFilterOnly() sent every non-owner
// single comparison down the filter-only path, where apply*Filter hard-errors
// on != for all fields except status/type, so `priority!=2`, `assignee!=x`,
// `label!=x`, `title!=x`, `id!=x`, date-field !=, etc. all failed even though
// their build*Predicate counterparts handle != fine.
func TestNeqRouting(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	// wantsPredicate: fields with no exclusion filter column route to the
	// predicate path. status/type have ExcludeStatus/ExcludeTypes so they stay
	// filter-only (RequiresPredicate=false); owner is always predicate-only.
	cases := []struct {
		query          string
		wantsPredicate bool
	}{
		{`priority!=2`, true},
		{`assignee!=alice`, true},
		{`label!=bug`, true},
		{`title!=foo`, true},
		{`desc!=foo`, true},
		{`notes!=foo`, true},
		{`id!=bd-1`, true},
		{`spec!=s1`, true},
		{`pinned!=true`, true},
		{`created!="2026-07-01"`, true},
		{`updated!="2026-07-01"`, true},
		{`closed!="2026-07-01"`, true},
		{`started!="2026-07-01"`, true},
		// status/type keep the filter-only fast path via Exclude* columns.
		{`status!=open`, false},
		{`type!=bug`, false},
		// owner is always predicate-only (pre-existing force-route).
		{`owner!=alice`, true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			res, err := EvaluateAt(tc.query, now)
			if err != nil {
				t.Fatalf("EvaluateAt(%q) unexpected error: %v", tc.query, err)
			}
			if res.RequiresPredicate != tc.wantsPredicate {
				t.Errorf("EvaluateAt(%q) RequiresPredicate=%v, want %v",
					tc.query, res.RequiresPredicate, tc.wantsPredicate)
			}
			if tc.wantsPredicate && res.Predicate == nil {
				t.Errorf("EvaluateAt(%q) routed to predicate mode but Predicate is nil", tc.query)
			}
		})
	}
}

// TestNeqPredicateBehavior verifies the routed predicates actually EXCLUDE the
// named value (not just parse without error) — the value-level assertion that
// distinguishes "routes somewhere" from "routes to the correct filter".
func TestNeqPredicateBehavior(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	pred := func(t *testing.T, q string) func(*types.Issue) bool {
		t.Helper()
		res, err := EvaluateAt(q, now)
		if err != nil {
			t.Fatalf("EvaluateAt(%q): %v", q, err)
		}
		if res.Predicate == nil {
			t.Fatalf("EvaluateAt(%q): expected a predicate", q)
		}
		return res.Predicate
	}

	t.Run("priority!=2 excludes P2, includes P1", func(t *testing.T) {
		p := pred(t, `priority!=2`)
		if p(&types.Issue{Priority: 2}) {
			t.Error("matched a P2 issue")
		}
		if !p(&types.Issue{Priority: 1}) {
			t.Error("failed to match a P1 issue")
		}
	})

	t.Run("assignee!=alice excludes alice (case-insensitive), includes bob", func(t *testing.T) {
		p := pred(t, `assignee!=alice`)
		if p(&types.Issue{Assignee: "Alice"}) {
			t.Error("matched assignee Alice")
		}
		if !p(&types.Issue{Assignee: "bob"}) {
			t.Error("failed to match assignee bob")
		}
	})

	t.Run("label!=bug excludes issue with bug, includes issue without", func(t *testing.T) {
		p := pred(t, `label!=bug`)
		if p(&types.Issue{Labels: []string{"bug", "p1"}}) {
			t.Error("matched an issue labelled bug")
		}
		if !p(&types.Issue{Labels: []string{"feature"}}) {
			t.Error("failed to match an issue not labelled bug")
		}
	})

	t.Run("id!=bd-1 excludes bd-1, includes bd-2", func(t *testing.T) {
		p := pred(t, `id!=bd-1`)
		if p(&types.Issue{ID: "bd-1"}) {
			t.Error("matched id bd-1")
		}
		if !p(&types.Issue{ID: "bd-2"}) {
			t.Error("failed to match id bd-2")
		}
	})

	t.Run("pinned!=true excludes pinned, includes unpinned", func(t *testing.T) {
		p := pred(t, `pinned!=true`)
		if p(&types.Issue{Pinned: true}) {
			t.Error("matched a pinned issue")
		}
		if !p(&types.Issue{Pinned: false}) {
			t.Error("failed to match an unpinned issue")
		}
	})
}

// TestNeqStillValidates confirms invalid values on a now-predicate-routed field
// still error (the fix must not open a validation hole): an out-of-range or
// non-numeric priority, and a bad boolean, must be rejected on the != path too.
func TestNeqStillValidates(t *testing.T) {
	for _, q := range []string{
		`priority!=abc`, // non-numeric
		`priority!=5`,   // out of 0-4 range
		`priority!=-1`,  // out of range
		`pinned!=maybe`, // invalid boolean
		`type!=bogus`,   // invalid type (stays on filter path, validates)
	} {
		if _, err := Evaluate(q); err == nil {
			t.Errorf("Evaluate(%q) should error, got nil", q)
		}
	}
}
