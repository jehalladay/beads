//go:build cgo

package main

import (
	"os"
	"testing"
)

// TestEmbeddedSearchSortBeforeLimit is the end-to-end teeth for beads-s4sn:
// `bd search --sort <key> --limit N` must return the true top-N in the
// requested order, not the top-N by the SQL default (priority) order merely
// re-displayed in the requested order.
//
// Before the fix, cmd/bd/search.go read --sort into a local var and applied
// sortIssues() CLIENT-SIDE, AFTER the SQL LIMIT, but never set
// filter.SortBy — so the LIMIT window was selected in the default priority
// order and the client only re-ordered those N rows. Asking for the 2
// alphabetically-first titles (with a smaller limit than the match count)
// silently returned the 2 highest-priority rows instead.
func TestEmbeddedSearchSortBeforeLimit(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "stl")

	// Title order is deliberately the INVERSE of priority order, so a
	// LIMIT-then-sort bug returns a different set than a sort-then-LIMIT.
	// Alphabetically-first titles have the LOWEST priority (P4); the
	// highest-priority rows (P0) sort LAST by title.
	bdCreate(t, bd, dir, "Apple task", "--type", "task", "--priority", "4")
	bdCreate(t, bd, dir, "Banana task", "--type", "task", "--priority", "4")
	bdCreate(t, bd, dir, "Cherry task", "--type", "task", "--priority", "0")
	bdCreate(t, bd, dir, "Date task", "--type", "task", "--priority", "0")

	// --sort title --limit 2 must return the two alphabetically-first titles.
	results := bdSearchJSON(t, bd, dir, "stl-", "--sort", "title", "--limit", "2")
	if len(results) != 2 {
		t.Fatalf("expected 2 results with --limit 2, got %d: %v", len(results), titlesOf(results))
	}
	got := titlesOf(results)
	want := []string{"Apple task", "Banana task"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort-before-limit broken (beads-s4sn): --sort title --limit 2 returned %v, want %v "+
				"(a LIMIT-then-sort bug returns the highest-priority rows, e.g. Cherry/Date, sorted by title)", got, want)
		}
	}

	// Reverse: the two alphabetically-LAST titles.
	revResults := bdSearchJSON(t, bd, dir, "stl-", "--sort", "title", "--reverse", "--limit", "2")
	if len(revResults) != 2 {
		t.Fatalf("expected 2 results with --reverse --limit 2, got %d", len(revResults))
	}
	revGot := titlesOf(revResults)
	revWant := []string{"Date task", "Cherry task"}
	for i := range revWant {
		if revGot[i] != revWant[i] {
			t.Fatalf("sort-before-limit --reverse broken (beads-s4sn): got %v, want %v", revGot, revWant)
		}
	}
}

// TestEmbeddedQuerySortBeforeLimit is the same teeth for the `bd query` path
// (cmd/bd/query.go), the primary query surface. The non-predicate query path
// had the identical beads-s4sn bug: --sort applied client-side after the SQL
// LIMIT, filter.SortBy never set. (The predicate path was already correct via
// beads-7hu4's page-all-then-sort.)
func TestEmbeddedQuerySortBeforeLimit(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "qtl")

	// Title order is the inverse of priority order (see search test above).
	bdCreate(t, bd, dir, "Apple task", "--type", "task", "--priority", "4")
	bdCreate(t, bd, dir, "Banana task", "--type", "task", "--priority", "4")
	bdCreate(t, bd, dir, "Cherry task", "--type", "task", "--priority", "0")
	bdCreate(t, bd, dir, "Date task", "--type", "task", "--priority", "0")

	// Non-predicate query: id=qtl-* matches all four; --sort title --limit 2
	// must return the two alphabetically-first titles, not the two
	// highest-priority ones.
	results := bdQueryJSON(t, bd, dir, `id="qtl-*"`, "--sort", "title", "--limit", "2")
	if len(results) != 2 {
		t.Fatalf("expected 2 results with --limit 2, got %d: %v", len(results), titlesOf(results))
	}
	got := titlesOf(results)
	want := []string{"Apple task", "Banana task"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query sort-before-limit broken (beads-s4sn): --sort title --limit 2 returned %v, want %v", got, want)
		}
	}
}

func titlesOf(results []map[string]interface{}) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		if t, ok := r["title"].(string); ok {
			out = append(out, t)
		} else {
			out = append(out, "")
		}
	}
	return out
}
