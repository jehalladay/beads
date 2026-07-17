package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// pageFakeStore embeds DoltStorage and implements only the two search methods
// the predicate page-loop uses. It serves a fixed slice honoring Offset+Limit
// (the contract beads-cand landed), so the loop can be exercised without a real
// database. It also counts SearchIssues calls to prove the loop actually pages.
type pageFakeStore struct {
	storage.DoltStorage
	all   []*types.Issue
	calls int
}

func (s *pageFakeStore) SearchIssues(_ context.Context, _ string, f types.IssueFilter) ([]*types.Issue, error) {
	s.calls++
	start := f.Offset
	if start > len(s.all) {
		start = len(s.all)
	}
	end := len(s.all)
	if f.Limit > 0 && start+f.Limit < end {
		end = start + f.Limit
	}
	return s.all[start:end], nil
}

func (s *pageFakeStore) SearchIssuesWithCounts(_ context.Context, _ string, f types.IssueFilter) ([]*types.IssueWithCounts, error) {
	issues, _ := s.SearchIssues(context.Background(), "", f)
	out := make([]*types.IssueWithCounts, len(issues))
	for i, is := range issues {
		out[i] = &types.IssueWithCounts{Issue: is}
	}
	return out, nil
}

// buildSpreadDataset returns n issues where only every kth issue matches the
// predicate, so the matches are spread across the whole table — well beyond any
// single over-fetch window. Matching issues have Priority == 0.
func buildSpreadDataset(n, k int) ([]*types.Issue, int) {
	all := make([]*types.Issue, 0, n)
	want := 0
	for i := 0; i < n; i++ {
		p := 1
		if i%k == 0 {
			p = 0
			want++
		}
		all = append(all, &types.Issue{ID: fmt.Sprintf("bd-%05d", i), Priority: p})
	}
	return all, want
}

// TestCollectPredicateMatches_PagesPastFirstWindow is the teeth for beads-7hu4:
// with matches spread across a table far larger than predicatePageSize, the
// loop must collect EVERY match, not just those in the first window. The old
// single-window fetch (limit*3, min 100) would return only matches inside the
// first 100 candidates; this asserts we get all of them.
func TestCollectPredicateMatches_PagesPastFirstWindow(t *testing.T) {
	// 1000 rows, every 7th matches => matches are spread to id ~994, far past
	// the first predicatePageSize(=100) window.
	all, want := buildSpreadDataset(1000, 7)
	if want <= predicatePageSize/7+1 {
		t.Fatalf("test misconfigured: want=%d not spread past the first window", want)
	}
	store := &pageFakeStore{all: all}

	pred := func(is *types.Issue) bool { return is.Priority == 0 }
	got, err := collectPredicateMatches(context.Background(), store, types.IssueFilter{}, "id", false, pred)
	if err != nil {
		t.Fatalf("collectPredicateMatches: %v", err)
	}
	if len(got) != want {
		t.Fatalf("collected %d matches, want %d (matches beyond the first window were dropped)", len(got), want)
	}
	// Must have paged: 1000 rows / 100 per page = 10 pages + a final short read.
	if store.calls < 10 {
		t.Fatalf("expected the loop to page (>=10 calls), got %d — no fill loop", store.calls)
	}
}

// TestCollectPredicateMatches_ShortPageStops verifies the loop terminates on a
// short (exhausted) page and does not spin.
func TestCollectPredicateMatches_ShortPageStops(t *testing.T) {
	all, want := buildSpreadDataset(150, 3) // 150 rows -> 2 pages (100 + 50)
	store := &pageFakeStore{all: all}
	pred := func(is *types.Issue) bool { return is.Priority == 0 }

	got, err := collectPredicateMatches(context.Background(), store, types.IssueFilter{}, "id", false, pred)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != want {
		t.Fatalf("got %d want %d", len(got), want)
	}
	if store.calls != 2 {
		t.Fatalf("expected exactly 2 page reads (100 then short 50), got %d", store.calls)
	}
}

// TestCollectPredicateMatchesWithCounts_PagesPastFirstWindow is the JSON-path
// twin of the teeth test.
func TestCollectPredicateMatchesWithCounts_PagesPastFirstWindow(t *testing.T) {
	all, want := buildSpreadDataset(1000, 7)
	store := &pageFakeStore{all: all}
	pred := func(is *types.Issue) bool { return is.Priority == 0 }

	got, err := collectPredicateMatchesWithCounts(context.Background(), store, types.IssueFilter{}, "id", false, pred)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != want {
		t.Fatalf("collected %d want %d", len(got), want)
	}
}

// TestPageFilterFor_StableSort verifies paging pins a deterministic order and
// resets the offset — required so successive windows don't overlap or skip.
func TestPageFilterFor_StableSort(t *testing.T) {
	f := pageFilterFor(types.IssueFilter{Limit: 3, Offset: 999}, "priority", true)
	if f.Limit != predicatePageSize {
		t.Fatalf("Limit = %d, want page size %d", f.Limit, predicatePageSize)
	}
	if f.Offset != 0 {
		t.Fatalf("Offset = %d, want 0 (paging starts at 0)", f.Offset)
	}
	if f.SortBy != "priority" || !f.SortDesc {
		t.Fatalf("sort not pinned to caller's order: SortBy=%q SortDesc=%v", f.SortBy, f.SortDesc)
	}
	// No caller sort -> fall back to a unique/total order (id) for safe paging.
	f2 := pageFilterFor(types.IssueFilter{}, "", false)
	if f2.SortBy != "id" {
		t.Fatalf("fallback SortBy = %q, want id", f2.SortBy)
	}
}
