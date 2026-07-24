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
		// beads-zykg2: honor the --json error contract, matching the direct path
		// (quick.go store.CreateIssue → HandleErrorRespectJSON, beads-wf68). Plain
		// FatalError left stdout empty under --json (fg6/65cgh/0igug class).
		FatalErrorRespectJSON("%v", err)
	}

	commandDidWrite.Store(true)

	// beads-bmvfn: fire on_create after the commit, matching the direct decorator
	// (HookFiringStore.CreateIssue → createHookEvents) and the proxied create path
	// (create_proxied_server.go, beads-w1vxy). The proxied UOW use-case layer
	// bypasses HookFiringStore, so a hub-connected crew's `bd q`/`bd quick`
	// on_create hook never ran. result.Issue does not carry Labels (they persist
	// via params.Labels + inheritance), so pass the merged explicit+inherited set
	// explicitly, mirroring the direct path's issue.Labels. Best-effort (warns to
	// stderr, does not fail the command).
	fireProxiedCreateHooks(ctx, result.Issue, mergeCreateLabels(labels, result.InheritedLabels))

	// beads-zykg2: under --json emit the full issue object, matching the direct
	// path (quick.go → outputJSON(issue), beads-j54e) and the proxied create path
	// (create_proxied_server.go). Previously this printed only result.Issue.ID
	// even under --json — byte-identical to plain output — breaking a scripted
	// `bd quick <t> --json` json.load consumer in hub/proxied mode.
	if jsonOutput {
		_ = outputJSON(result.Issue)
		return
	}
	fmt.Println(result.Issue.ID)
}
