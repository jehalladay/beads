package main

import (
	"context"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// Proxied-server predicate-path paging (beads-j1sh, sibling of beads-7hu4).
//
// The --proxied-server query path (query_proxied_server.go) had the same
// under-return as the direct path: a complex predicate could not be pushed into
// SQL, so the use-case returned a single limit*3 (min 100) candidate window, the
// predicate was applied in memory, and the result was truncated — with no fill
// loop. A selective predicate over a large table therefore silently returned
// fewer than the limit. Worse than the direct path: the truncation hint was
// suppressed, because `truncated` was recomputed as len(survivors) > limit,
// which discarded the use-case's page.HasMore — so when the single window
// yielded fewer survivors than the limit (the selective case), the user got no
// signal at all.
//
// These helpers page through the use-case via IssueFilter.Offset in a stable
// order, apply the predicate to every page, and accumulate ALL survivors until
// the use-case reports no more rows (or a short page) or the scan cap is hit.
// The caller sorts the full survivor set, truncates to the limit, and derives
// the truncation hint from the TRUE overflow (survivors beyond the limit, or the
// cap being reached) rather than from a single window.
//
// This file is self-contained (its own paging constants + filter helper) so the
// proxied-path fix lands independently of the direct-path fix (beads-7hu4),
// which the merge queue may order separately.

const (
	// proxiedPredicatePageSize is the per-page candidate fetch size.
	proxiedPredicatePageSize = 100
	// proxiedMaxPredicateScan bounds total rows scanned across all pages.
	proxiedMaxPredicateScan = 100_000
)

// proxiedPageFilterFor returns a copy of base prepared for stable Offset paging:
// fixed page size and a pinned deterministic sort so successive Offset windows
// neither overlap nor skip rows.
func proxiedPageFilterFor(base types.IssueFilter, sortBy string, reverse bool) types.IssueFilter {
	f := base
	f.Limit = proxiedPredicatePageSize
	f.Offset = 0
	if sortBy != "" {
		f.SortBy = sortBy
		f.SortDesc = reverse
	} else {
		f.SortBy = "id"
		f.SortDesc = false
	}
	return f
}

// proxiedPredicateResult is the outcome of a paged predicate scan on the
// proxied path. Truncated reports whether more matches exist than were returned
// (either the scan cap was hit, or survivors exceeded the requested limit).
type proxiedPredicateResultIssues struct {
	items     []*types.Issue
	capReached bool
}

type proxiedPredicateResultCounts struct {
	items      []*types.IssueWithCounts
	capReached bool
}

// collectProxiedPredicateMatches pages domain.IssueUseCase.SearchIssues applying
// pred to every candidate, returning ALL survivors plus whether the scan cap was
// hit (matches may exist beyond what was scanned).
func collectProxiedPredicateMatches(
	ctx context.Context,
	uc domain.IssueUseCase,
	base types.IssueFilter,
	sortBy string,
	reverse bool,
	pred func(*types.Issue) bool,
) (proxiedPredicateResultIssues, error) {
	f := proxiedPageFilterFor(base, sortBy, reverse)
	var out []*types.Issue
	scanned := 0
	for {
		if scanned >= proxiedMaxPredicateScan {
			return proxiedPredicateResultIssues{items: out, capReached: true}, nil
		}
		page, err := uc.SearchIssues(ctx, "", f)
		if err != nil {
			return proxiedPredicateResultIssues{}, err
		}
		if len(page.Items) == 0 {
			break
		}
		for _, issue := range page.Items {
			if pred == nil || pred(issue) {
				out = append(out, issue)
			}
		}
		scanned += len(page.Items)
		if !page.HasMore || len(page.Items) < f.Limit {
			break // use-case reports the store is exhausted
		}
		f.Offset += len(page.Items)
	}
	return proxiedPredicateResultIssues{items: out, capReached: false}, nil
}

// collectProxiedPredicateMatchesWithCounts is the *types.IssueWithCounts variant
// used by the JSON output path.
func collectProxiedPredicateMatchesWithCounts(
	ctx context.Context,
	uc domain.IssueUseCase,
	base types.IssueFilter,
	sortBy string,
	reverse bool,
	pred func(*types.Issue) bool,
) (proxiedPredicateResultCounts, error) {
	f := proxiedPageFilterFor(base, sortBy, reverse)
	var out []*types.IssueWithCounts
	scanned := 0
	for {
		if scanned >= proxiedMaxPredicateScan {
			return proxiedPredicateResultCounts{items: out, capReached: true}, nil
		}
		page, err := uc.SearchIssuesWithCounts(ctx, "", f)
		if err != nil {
			return proxiedPredicateResultCounts{}, err
		}
		if len(page.Items) == 0 {
			break
		}
		for _, item := range page.Items {
			if item == nil || item.Issue == nil {
				continue
			}
			if pred == nil || pred(item.Issue) {
				out = append(out, item)
			}
		}
		scanned += len(page.Items)
		if !page.HasMore || len(page.Items) < f.Limit {
			break
		}
		f.Offset += len(page.Items)
	}
	return proxiedPredicateResultCounts{items: out, capReached: false}, nil
}
