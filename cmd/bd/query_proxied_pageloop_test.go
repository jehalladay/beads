package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// pageFakeUseCase embeds domain.IssueUseCase and implements only the two search
// methods the proxied predicate page-loop uses. It serves a fixed slice honoring
// Offset+Limit and reports HasMore when more rows remain past the window — the
// contract the real use-case provides (beads-cand landed Offset on this path).
type pageFakeUseCase struct {
	domain.IssueUseCase
	all   []*types.Issue
	calls int
}

func (u *pageFakeUseCase) SearchIssues(_ context.Context, _ string, f types.IssueFilter) (domain.SearchPage, error) {
	u.calls++
	start := f.Offset
	if start > len(u.all) {
		start = len(u.all)
	}
	end := len(u.all)
	if f.Limit > 0 && start+f.Limit < end {
		end = start + f.Limit
	}
	return domain.SearchPage{Items: u.all[start:end], HasMore: end < len(u.all)}, nil
}

func (u *pageFakeUseCase) SearchIssuesWithCounts(ctx context.Context, q string, f types.IssueFilter) (domain.SearchCountsPage, error) {
	p, _ := u.SearchIssues(ctx, q, f)
	out := make([]*types.IssueWithCounts, len(p.Items))
	for i, is := range p.Items {
		out[i] = &types.IssueWithCounts{Issue: is}
	}
	return domain.SearchCountsPage{Items: out, HasMore: p.HasMore}, nil
}

func spreadIssues(n, k int) ([]*types.Issue, int) {
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

// TestCollectProxiedPredicateMatches_PagesPastFirstWindow is the teeth for
// beads-j1sh: with matches spread across a table far larger than the page size,
// the loop must collect EVERY match, not just those in the first window. The old
// single-window fetch returned only matches inside the first limit*3 (min 100)
// window; this asserts we get all of them.
func TestCollectProxiedPredicateMatches_PagesPastFirstWindow(t *testing.T) {
	all, want := spreadIssues(1000, 7)
	uc := &pageFakeUseCase{all: all}
	pred := func(is *types.Issue) bool { return is.Priority == 0 }

	res, err := collectProxiedPredicateMatches(context.Background(), uc, types.IssueFilter{}, "id", false, pred)
	if err != nil {
		t.Fatalf("collectProxiedPredicateMatches: %v", err)
	}
	if len(res.items) != want {
		t.Fatalf("collected %d matches, want %d (matches beyond the first window were dropped)", len(res.items), want)
	}
	if res.capReached {
		t.Fatalf("capReached=true for a %d-row table, want false", len(all))
	}
	if uc.calls < 10 {
		t.Fatalf("expected the loop to page (>=10 calls), got %d — no fill loop", uc.calls)
	}
}

// TestCollectProxiedPredicateMatchesWithCounts_PagesPastFirstWindow is the
// JSON-path twin.
func TestCollectProxiedPredicateMatchesWithCounts_PagesPastFirstWindow(t *testing.T) {
	all, want := spreadIssues(1000, 7)
	uc := &pageFakeUseCase{all: all}
	pred := func(is *types.Issue) bool { return is.Priority == 0 }

	res, err := collectProxiedPredicateMatchesWithCounts(context.Background(), uc, types.IssueFilter{}, "id", false, pred)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.items) != want {
		t.Fatalf("collected %d want %d", len(res.items), want)
	}
}

// TestCollectProxiedPredicateMatches_ShortPageStops verifies termination on an
// exhausted (HasMore=false / short) page.
func TestCollectProxiedPredicateMatches_ShortPageStops(t *testing.T) {
	all, want := spreadIssues(150, 3) // 2 pages: 100 (HasMore) + 50 (exhausted)
	uc := &pageFakeUseCase{all: all}
	pred := func(is *types.Issue) bool { return is.Priority == 0 }

	res, err := collectProxiedPredicateMatches(context.Background(), uc, types.IssueFilter{}, "id", false, pred)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.items) != want {
		t.Fatalf("got %d want %d", len(res.items), want)
	}
	if uc.calls != 2 {
		t.Fatalf("expected exactly 2 page reads, got %d", uc.calls)
	}
}

// TestProxiedPageFilterFor_StableSort verifies paging pins a deterministic order
// and resets the offset.
func TestProxiedPageFilterFor_StableSort(t *testing.T) {
	f := proxiedPageFilterFor(types.IssueFilter{Limit: 3, Offset: 999}, "priority", true)
	if f.Limit != proxiedPredicatePageSize {
		t.Fatalf("Limit = %d, want %d", f.Limit, proxiedPredicatePageSize)
	}
	if f.Offset != 0 {
		t.Fatalf("Offset = %d, want 0", f.Offset)
	}
	if f.SortBy != "priority" || !f.SortDesc {
		t.Fatalf("sort not pinned: SortBy=%q SortDesc=%v", f.SortBy, f.SortDesc)
	}
	if f2 := proxiedPageFilterFor(types.IssueFilter{}, "", false); f2.SortBy != "id" {
		t.Fatalf("fallback SortBy = %q, want id", f2.SortBy)
	}
}
