package query

import (
	"testing"
	"time"
)

// TestStatusBlockedRoutesToBlockedFilter pins beads-uakf2: the query DSL must
// route the derived "blocked" pseudo-status to filter.Blocked (the denormalized
// is_blocked column), NOT to the status column. Before the fix,
// applyStatusFilter set filter.Status=&"blocked" (and applyNot appended
// "blocked" to ExcludeStatus), emitting an unsatisfiable `status = 'blocked'`
// WHERE clause — a blocked issue keeps stored status open/in_progress, so
// `bd query status=blocked` silently returned 0 (rc=0). This is the same
// pseudo-status-reaches-a-stored-status-predicate class as beads-3x0e4
// (find-duplicates/human/search), beads-7f3g (list/count), beads-h40fl (stale),
// beads-pbelp (lint), and beads-s5ha0 (migrate) — but on the query DSL
// evaluator, a separate path the cobra --status-flag fixes did not touch.
func TestStatusBlockedRoutesToBlockedFilter(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	assertBlocked := func(t *testing.T, query string, wantVal bool) {
		t.Helper()
		res, err := EvaluateAt(query, now)
		if err != nil {
			t.Fatalf("EvaluateAt(%q) unexpected err: %v", query, err)
		}
		// Must NOT route to the stored-status column (the bug).
		if res.Filter.Status != nil {
			t.Errorf("EvaluateAt(%q): Filter.Status = %q, want nil (blocked must not reach the status column)", query, *res.Filter.Status)
		}
		for _, s := range res.Filter.ExcludeStatus {
			if s == "blocked" {
				t.Errorf("EvaluateAt(%q): ExcludeStatus contains \"blocked\" (unsatisfiable status <> 'blocked'), want blocked routed to Filter.Blocked", query)
			}
		}
		// Must route to the is_blocked pseudo-status column with the right sense.
		if res.Filter.Blocked == nil {
			t.Fatalf("EvaluateAt(%q): Filter.Blocked = nil, want %v (blocked must route to the is_blocked filter)", query, wantVal)
		}
		if *res.Filter.Blocked != wantVal {
			t.Errorf("EvaluateAt(%q): Filter.Blocked = %v, want %v", query, *res.Filter.Blocked, wantVal)
		}
		// The whole point: a bare blocked comparison stays on the filter-only
		// SQL fast path (no in-memory predicate), so the is_blocked WHERE clause
		// actually runs.
		if res.RequiresPredicate {
			t.Errorf("EvaluateAt(%q): RequiresPredicate = true, want false (bare status=blocked must take the filter-only path)", query)
		}
	}

	t.Run("status=blocked -> only-blocked", func(t *testing.T) {
		assertBlocked(t, "status=blocked", true)
	})
	t.Run("status!=blocked -> only-unblocked", func(t *testing.T) {
		assertBlocked(t, "status!=blocked", false)
	})
	t.Run("NOT status=blocked -> only-unblocked", func(t *testing.T) {
		assertBlocked(t, "NOT status=blocked", false)
	})

	// Non-blocked statuses must be UNAFFECTED: they still route to the status
	// column and must NOT set Filter.Blocked (no over-reach from the new branch).
	t.Run("status=open unaffected", func(t *testing.T) {
		res, err := EvaluateAt("status=open", now)
		if err != nil {
			t.Fatalf("EvaluateAt(status=open): %v", err)
		}
		if res.Filter.Blocked != nil {
			t.Errorf("status=open: Filter.Blocked = %v, want nil (only blocked routes there)", *res.Filter.Blocked)
		}
		if res.Filter.Status == nil || *res.Filter.Status != "open" {
			t.Errorf("status=open: Filter.Status = %v, want \"open\"", res.Filter.Status)
		}
	})
	t.Run("status!=open unaffected (ExcludeStatus)", func(t *testing.T) {
		res, err := EvaluateAt("status!=open", now)
		if err != nil {
			t.Fatalf("EvaluateAt(status!=open): %v", err)
		}
		if res.Filter.Blocked != nil {
			t.Errorf("status!=open: Filter.Blocked = %v, want nil", *res.Filter.Blocked)
		}
		if len(res.Filter.ExcludeStatus) != 1 || res.Filter.ExcludeStatus[0] != "open" {
			t.Errorf("status!=open: ExcludeStatus = %v, want [open]", res.Filter.ExcludeStatus)
		}
	})
}
