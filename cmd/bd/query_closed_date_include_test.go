package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/query"
)

// TestHasExplicitStatusFilter_ClosedDatePredicate is the teeth for beads-6dgb3.
//
// A bare closed-date predicate (`closed>7d`, `closed=DATE`, the `closed_at`
// alias) always returned empty: query.go:186 appends ExcludeStatus=closed by
// default unless the query carries explicit intent-to-include-closed, and
// hasExplicitStatusFilter recognized only a `status` comparison as that intent.
// The closed date field is set ONLY on closed issues (closed_at non-NULL <->
// status=closed exactly), so default-excluding closed rows strips the exact
// rows a closed-date predicate could match — the query self-nullifies.
//
// A comparison on `closed`/`closed_at` must therefore count as
// intent-to-include-closed. SCOPE guard: started/updated/created predicates
// must NOT (their rows survive default-exclusion and carry those timestamps),
// and a plain `status`/non-date leg keeps its prior behavior.
func TestHasExplicitStatusFilter_ClosedDatePredicate(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  bool
	}{
		// beads-6dgb3: closed date field disables default closed-exclusion.
		{"bare closed>Nd", "closed>7d", true},
		{"bare closed<Nd", "closed<30d", true},
		{"bare closed=date", `closed="2026-07-01"`, true},
		{"closed_at alias", "closed_at>7d", true},

		// Direct status intent (pre-existing behavior).
		{"status=closed", "status=closed", true},
		{"status=open", "status=open", true},

		// Scope guard: other date fields must NOT self-nullify, so they do
		// NOT count as include-closed intent (their rows survive exclusion).
		{"created>30d bare", "created>30d", false},
		{"updated>7d bare", "updated>7d", false},
		{"started>7d bare", "started>7d", false},

		// Non-status, non-closed leaf is unaffected.
		{"priority leg", "priority=1", false},

		// Compound: a closed leg anywhere in the tree carries the intent.
		{"AND with closed", "priority=1 AND closed>7d", true},
		{"OR with closed", "status=open OR closed_at<30d", true},
		{"NOT closed", "NOT closed>7d", true},

		// Compound with no closed/status leg stays false.
		{"AND without closed", "priority=1 AND created>7d", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node, err := query.Parse(tc.query)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.query, err)
			}
			if got := hasExplicitStatusFilter(node); got != tc.want {
				t.Errorf("hasExplicitStatusFilter(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}
