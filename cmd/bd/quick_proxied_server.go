package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// runQuickProxiedServer creates an issue via the proxied unit-of-work stack for
// hub-connected crew, where the global `store` is nil (beads-eh0z, fszd/aocj
// umbrella). It mirrors `bd create` (create_proxied_server.go): build
// CreateIssueParams and run IssueUseCase().CreateIssue inside a transaction,
// then print only the new ID (the `bd q` contract). Labels are carried on
// params.Labels (that is what domain create() persists via labelRepo, not
// issue.Labels), matching the create path.
func runQuickProxiedServer(ctx context.Context, issue *types.Issue, labels []string) {
	params := domain.CreateIssueParams{
		Issue:  issue,
		Labels: mergeCreateLabels(labels, nil),
	}

	var result domain.CreateIssueResult
	if err := uow.RunInTxMsg(ctx, uowProvider, func(uw uow.UnitOfWork) (string, error) {
		var e error
		result, e = uw.IssueUseCase().CreateIssue(ctx, params, actor)
		if e != nil {
			return "", e
		}
		return fmt.Sprintf("bd: create %s", result.Issue.ID), nil
	}); err != nil {
		FatalError("%v", err)
	}

	commandDidWrite.Store(true)
	fmt.Println(result.Issue.ID)
}
