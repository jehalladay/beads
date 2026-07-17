package main

import (
	"context"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// Predicate-path paging (beads-7hu4).
//
// A `bd query <predicate> --limit N` with a complex predicate (RequiresPredicate)
// cannot be pushed into SQL, so the store returns a candidate window and the
// predicate is applied in memory. The old code fetched a single window of
// limit*3 (min 100) candidates, filtered, and truncated to N — with NO fill
// loop. A selective predicate over a large table therefore silently returned
// FEWER than N matches (true matches beyond the candidate window were dropped),
// and because the truncation happened before the final sort, the N it did
// return were not the sorted-top-N.
//
// These helpers close both halves: they page through the store via
// IssueFilter.Offset (landed by beads-cand) in a STABLE sort order, applying the
// predicate to every page and accumulating ALL survivors, until the store is
// exhausted or a hard scan cap is reached. The caller then sorts the full
// survivor set and truncates to the limit — so the result is the true
// sorted-top-N, not a prefix of an arbitrary window.
//
// A hard cap (maxPredicateScan) bounds worst-case work for a predicate that
// matches almost nothing over a huge table; when the cap is hit the result is
// still correct for everything scanned and is the documented, bounded behavior
// rather than an unbounded full-table scan on every query.

const (
	// predicatePageSize is the per-page candidate fetch size for the predicate
	// paging loop. Larger pages mean fewer round-trips; this mirrors the old
	// single-window floor of 100.
	predicatePageSize = 100

	// maxPredicateScan bounds the total number of candidate rows the predicate
	// loop will scan across all pages, so a highly selective predicate over a
	// very large table cannot turn one query into an unbounded full scan.
	maxPredicateScan = 100_000
)

// pageFilterFor returns a copy of the base filter prepared for stable Offset
// paging: it fixes the page size and pins a deterministic sort order so
// successive Offset windows do not overlap or skip rows. The store may return
// rows in an unspecified order otherwise, which makes Offset paging unsafe.
func pageFilterFor(base types.IssueFilter, sortBy string, reverse bool) types.IssueFilter {
	f := base
	f.Limit = predicatePageSize
	f.Offset = 0
	// Pin a stable order for paging. Prefer the caller's requested sort so the
	// pages come back in roughly the final order; fall back to id which is
	// unique and total (guarantees no page overlap/skip).
	if sortBy != "" {
		f.SortBy = sortBy
		f.SortDesc = reverse
	} else {
		f.SortBy = "id"
		f.SortDesc = false
	}
	return f
}

// collectPredicateMatches pages through store.SearchIssues applying pred to
// every candidate, returning ALL survivors (unsorted, untruncated). Paging stops
// when the store returns a short page (exhausted) or the scan cap is reached.
func collectPredicateMatches(
	ctx context.Context,
	store storage.DoltStorage,
	base types.IssueFilter,
	sortBy string,
	reverse bool,
	pred func(*types.Issue) bool,
) ([]*types.Issue, error) {
	f := pageFilterFor(base, sortBy, reverse)
	var out []*types.Issue
	scanned := 0
	for scanned < maxPredicateScan {
		page, err := store.SearchIssues(ctx, "", f)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, issue := range page {
			if pred == nil || pred(issue) {
				out = append(out, issue)
			}
		}
		scanned += len(page)
		if len(page) < f.Limit {
			break // last (short) page: store exhausted
		}
		f.Offset += len(page)
	}
	return out, nil
}

// collectPredicateMatchesWithCounts is the *types.IssueWithCounts variant used
// by the JSON output path. It mirrors collectPredicateMatches exactly.
func collectPredicateMatchesWithCounts(
	ctx context.Context,
	store storage.DoltStorage,
	base types.IssueFilter,
	sortBy string,
	reverse bool,
	pred func(*types.Issue) bool,
) ([]*types.IssueWithCounts, error) {
	f := pageFilterFor(base, sortBy, reverse)
	var out []*types.IssueWithCounts
	scanned := 0
	for scanned < maxPredicateScan {
		page, err := store.SearchIssuesWithCounts(ctx, "", f)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, item := range page {
			if item == nil || item.Issue == nil {
				continue
			}
			if pred == nil || pred(item.Issue) {
				out = append(out, item)
			}
		}
		scanned += len(page)
		if len(page) < f.Limit {
			break
		}
		f.Offset += len(page)
	}
	return out, nil
}
