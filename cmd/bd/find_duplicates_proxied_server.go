package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/types"
)

// fetchFindDuplicatesIssuesProxied fetches the candidate issue set for
// bd find-duplicates via the proxied unit-of-work stack, for hub-connected crew
// where the global `store` is nil (beads-zawz). It mirrors the direct path's
// fetch (find_duplicates.go): validate --status against the same custom-status
// config (loadProxiedListFilterConfig) and run SearchIssues through the UOW
// IssueUseCase — the same usecase the landed search/list/ready proxied handlers
// use. The client-side pairing/render in runFindDuplicates is store-free and is
// shared unchanged.
func fetchFindDuplicatesIssuesProxied(ctx context.Context, status string) ([]*types.Issue, error) {
	uw, err := openProxiedListUOW(ctx)
	if err != nil {
		return nil, err
	}
	defer uw.Close(ctx)

	filter := types.IssueFilter{}
	if status != "" && status != "all" {
		cfg, cfgErr := loadProxiedListFilterConfig(ctx, uw)
		if cfgErr != nil {
			return nil, cfgErr
		}
		s := types.Status(status).Normalize()
		if !s.IsValidWithCustom(cfg.customStatusNames()) {
			return nil, fmt.Errorf("invalid status %q (valid: %s)", status, validStatusList(cfg.customStatusNames()))
		}
		filter.Status = &s
	}

	page, err := uw.IssueUseCase().SearchIssues(ctx, "", filter)
	if err != nil {
		return nil, fmt.Errorf("fetching issues: %v", err)
	}
	return page.Items, nil
}
