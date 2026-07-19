package main

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// fetchDependencyCountsForDuplicates returns per-issue dependency counts for the
// bd duplicates structural-scoring pass, proxied-aware (beads-igmz). In
// proxiedServerMode the global `store` is nil, so route through the UOW
// DependencyUseCase().CountsByIssueIDs (the same usecase the search proxied
// handler uses); otherwise use the direct store.
func fetchDependencyCountsForDuplicates(ctx context.Context, issueIDs []string) (map[string]*types.DependencyCounts, error) {
	if usesProxiedServer() {
		uw, err := openProxiedListUOW(ctx)
		if err != nil {
			return nil, err
		}
		defer uw.Close(ctx)
		return uw.DependencyUseCase().CountsByIssueIDs(ctx, issueIDs)
	}
	return store.GetDependencyCounts(ctx, issueIDs)
}

// fetchDuplicatesIssuesProxied fetches the full issue set for bd duplicates via
// the proxied unit-of-work stack, for hub-connected crew where the global
// `store` is nil (beads-igmz). It mirrors the direct path's read fetch
// (duplicates.go: store.SearchIssues with an empty filter) through the UOW
// IssueUseCase — the same usecase the landed search/list/find-duplicates
// proxied handlers use. The client-side grouping/render is store-free and is
// shared unchanged. The --auto-merge write path is gated off in proxied mode by
// the caller (performMerge needs GetDependentsWithMetadata, not yet on the UOW —
// beads-crys).
func fetchDuplicatesIssuesProxied(ctx context.Context) ([]*types.Issue, error) {
	uw, err := openProxiedListUOW(ctx)
	if err != nil {
		return nil, err
	}
	defer uw.Close(ctx)

	page, err := uw.IssueUseCase().SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}
