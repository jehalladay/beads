package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/types"
)

// runSearchProxiedServer is the proxied-server path for bd search (beads-iq3i).
// The direct RunE dereferences the nil global `store` (SearchIssues,
// GetLabelsForIssues, GetDependencyCounts, GetCommentCounts) which panics on a
// hub-connected crew. This is a clean-mirror leg: every store method maps to a
// UOW usecase already used by the landed list/ready proxied handlers, and
// loadProxiedListFilterConfig(uw) supplies the same custom status/type set.
func runSearchProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) error {
	query, err := parseSearchQuery(cmd, args)
	if err != nil {
		return err
	}

	uw, err := openProxiedListUOW(ctx)
	if err != nil {
		return err
	}
	defer uw.Close(ctx)

	cfg, err := loadProxiedListFilterConfig(ctx, uw)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	params, err := parseSearchParams(cmd, cfg)
	if err != nil {
		return err
	}

	page, err := uw.IssueUseCase().SearchIssues(ctx, query, params.filter)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	issues := page.Items

	sortIssues(issues, params.sortBy, params.reverse)

	if jsonOutput {
		issueIDs := searchIssueIDs(issues)
		labelsMap, err := uw.LabelUseCase().GetLabelsForIssues(ctx, issueIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get labels: %v\n", err)
			labelsMap = make(map[string][]string)
		}
		depCounts, err := uw.DependencyUseCase().CountsByIssueIDs(ctx, issueIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get dependency counts: %v\n", err)
			depCounts = make(map[string]*types.DependencyCounts)
		}
		commentCounts, err := uw.CommentUseCase().GetCommentCounts(ctx, issueIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get comment counts: %v\n", err)
			commentCounts = make(map[string]int)
		}
		return outputJSON(buildSearchIssuesWithCounts(issues, labelsMap, depCounts, commentCounts))
	}

	labelsMap, _ := uw.LabelUseCase().GetLabelsForIssues(ctx, searchIssueIDs(issues))
	for _, issue := range issues {
		issue.Labels = labelsMap[issue.ID]
	}
	// beads-4wn0: report the TRUE match count (not the --limit-truncated page
	// size). Mirror the direct path: re-query with Limit=0 when the page fills.
	totalMatches := len(issues)
	if params.filter.Limit > 0 && len(issues) == params.filter.Limit {
		countFilter := params.filter
		countFilter.Limit = 0
		if allPage, cErr := uw.IssueUseCase().SearchIssues(ctx, query, countFilter); cErr == nil && len(allPage.Items) > len(issues) {
			totalMatches = len(allPage.Items)
		}
	}
	outputSearchResults(issues, query, params.longFormat, totalMatches)
	return nil
}
