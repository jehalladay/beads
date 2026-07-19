package main

import (
	"context"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// beads-mtgy: proxied-server READ handler for `bd status` (alias `bd stats`).
// The direct RunE reads via the global `store` (GetStatistics / SearchIssues /
// GetReadyWork), which is NIL in proxiedServerMode (main.go PersistentPreRun sets
// uowProvider and returns BEFORE `store = newDoltStore`), so a hub-connected crew
// gets "storage is nil" instead of the DB overview. Route through the UOW
// IssueUseCase, mirroring the state/graph proxied read handlers (i3hq/jc8k class).
// Clean-mirror: GetStatistics/SearchIssues/GetReadyWork already exist on
// IssueUseCase — no interface extension.

// runStatusProxiedServer implements `bd status` under proxied-server mode.
// Mirrors the direct statusCmd RunE output exactly (both --json and human forms).
func runStatusProxiedServer(ctx context.Context, showAssigned, noActivity bool) error {
	if uowProvider == nil {
		return HandleErrorRespectJSON("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()

	var stats *types.Statistics
	if showAssigned {
		stats, err = getAssignedStatisticsProxied(ctx, issueUC, actor)
	} else {
		stats, err = issueUC.GetStatistics(ctx)
	}
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	var recentActivity *RecentActivitySummary
	if !noActivity {
		recentActivity = getGitActivity(24)
	}

	output := &StatusOutput{
		Summary:        stats,
		RecentActivity: recentActivity,
	}

	if jsonOutput {
		return outputJSON(output)
	}

	renderStatusHuman(stats, recentActivity)
	return nil
}

// getAssignedStatisticsProxied mirrors getAssignedStatistics (status.go) but reads
// through the UOW IssueUseCase. SearchIssues/GetReadyWork return a SearchPage;
// use .Items for the issue slice.
func getAssignedStatisticsProxied(ctx context.Context, issueUC domain.IssueUseCase, assignee string) (*types.Statistics, error) {
	assigneePtr := assignee
	filter := types.IssueFilter{
		Assignee: &assigneePtr,
	}

	page, err := issueUC.SearchIssues(ctx, "", filter)
	if err != nil {
		return nil, err
	}

	stats := &types.Statistics{
		TotalIssues: len(page.Items),
	}
	for _, issue := range page.Items {
		switch issue.Status {
		case types.StatusOpen:
			stats.OpenIssues++
		case types.StatusInProgress:
			stats.InProgressIssues++
		case types.StatusBlocked:
			stats.BlockedIssues++
		case types.StatusDeferred:
			stats.DeferredIssues++
		case types.StatusClosed:
			stats.ClosedIssues++
		}
	}

	readyFilter := types.WorkFilter{
		Assignee: &assigneePtr,
	}
	if readyPage, err := issueUC.GetReadyWork(ctx, readyFilter); err == nil {
		stats.ReadyIssues = len(readyPage.Items)
	}

	return stats, nil
}
