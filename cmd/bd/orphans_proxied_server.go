package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/types"
)

// beads-ktlo: proxied-server READ provider for `bd orphans`.
// The direct path (orphans.go) builds a doltStoreProvider backed by the global
// `store`, which is NIL in proxiedServerMode (main.go PersistentPreRun sets
// uowProvider and returns BEFORE `store = newDoltStore`). getIssueProviderFn
// then returns "no database available" on hub-connected crew instead of listing
// orphaned issues. Route through the UOW use-cases instead (jc8k/i3hq/mtgy
// read-divergence class).
//
// Clean-mirror: the IssueProvider interface needs only GetOpenIssues
// (store.SearchIssues → IssueUseCase.SearchIssues) and GetIssuePrefix
// (store.GetConfig → ConfigUseCase.GetConfig). Both already exist on the UOW,
// so no interface extension is required. IssueUseCase.SearchIssues returns a
// domain.SearchPage — use .Items for the []*types.Issue.

// proxiedIssueProvider implements types.IssueProvider over the proxied UOW stack.
// A fresh unit-of-work is opened per read (orphan detection issues at most two
// SearchIssues calls plus one prefix lookup, all read-only).
type proxiedIssueProvider struct {
	labels    []string // AND semantics: issue must have ALL these labels
	labelsAny []string // OR semantics: issue must have AT LEAST ONE of these labels
}

func (p *proxiedIssueProvider) GetOpenIssues(ctx context.Context) ([]*types.Issue, error) {
	if uowProvider == nil {
		return nil, fmt.Errorf("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return nil, fmt.Errorf("open unit of work: %w", err)
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()

	openStatus := types.StatusOpen
	openPage, err := issueUC.SearchIssues(ctx, "", types.IssueFilter{
		Status:    &openStatus,
		Labels:    p.labels,
		LabelsAny: p.labelsAny,
	})
	if err != nil {
		return nil, err
	}
	inProgressStatus := types.StatusInProgress
	inProgressPage, err := issueUC.SearchIssues(ctx, "", types.IssueFilter{
		Status:    &inProgressStatus,
		Labels:    p.labels,
		LabelsAny: p.labelsAny,
	})
	if err != nil {
		return nil, err
	}
	return append(openPage.Items, inProgressPage.Items...), nil
}

func (p *proxiedIssueProvider) GetIssuePrefix() string {
	// YAML config takes precedence — in shared-server mode the DB
	// may belong to a different project (GH#2469). Mirrors doltStoreProvider.
	if yamlPrefix := config.GetString("issue-prefix"); yamlPrefix != "" {
		return yamlPrefix
	}
	if uowProvider == nil {
		return "bd"
	}
	ctx := context.Background()
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return "bd"
	}
	defer uw.Close(ctx)
	prefix, err := uw.ConfigUseCase().GetConfig(ctx, "issue_prefix")
	if err != nil || prefix == "" {
		return "bd"
	}
	return prefix
}
