package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// ensureProxiedUOWProvider returns the process-wide proxied-server UOW provider,
// lazily creating it when it was not initialized during startup.
//
// Most commands get `uowProvider` set up in main.go's PersistentPreRunE. The
// `human` command family is in noDbCommands, so that init is skipped (both the
// direct `store` and `uowProvider` stay nil). This helper lets a human
// subcommand that needs to write in proxied-server mode build the provider on
// demand, mirroring the main.go init path (beads-ivje).
func ensureProxiedUOWProvider(ctx context.Context) (uow.UnitOfWorkProvider, error) {
	if uowProvider != nil {
		return uowProvider, nil
	}
	beadsDir := beads.FindBeadsDir()
	if beadsDir == "" {
		return nil, fmt.Errorf("no beads database found.\n" +
			"Hint: run 'bd init' to create a database in the current directory")
	}
	p, err := newProxiedServerUOWProvider(ctx, beadsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open uow provider: %w", err)
	}
	uowProvider = p
	return uowProvider, nil
}

// runHumanDismissProxiedServer closes a human-needed bead through the proxied
// UOW (beads-ivje). It mirrors the direct-path dismiss handler: refuse an
// already-closed issue, warn (non-fatally) if the bead lacks the "human" label,
// then close with a "Dismissed"/"Dismissed: <reason>" reason.
func runHumanDismissProxiedServer(ctx context.Context, issueID, reason string) error {
	provider, err := ensureProxiedUOWProvider(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	uw, err := provider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	issue, isWisp := proxiedResolveIssueOrWisp(ctx, uw, issueID)
	if issue == nil {
		return HandleErrorRespectJSON("issue not found: %s", issueID)
	}
	resolvedID := issue.ID

	if issue.Status == types.StatusClosed {
		return HandleErrorRespectJSON("issue %s is already closed", resolvedID)
	}

	if labels, lerr := uw.LabelUseCase().GetLabels(ctx, resolvedID); lerr == nil {
		hasHumanLabel := false
		for _, label := range labels {
			if label == "human" {
				hasHumanLabel = true
				break
			}
		}
		if !hasHumanLabel {
			fmt.Fprintf(os.Stderr, "Warning: Issue %s does not have 'human' label\n", resolvedID)
		}
	}

	closeReason := "Dismissed"
	if reason != "" {
		closeReason = fmt.Sprintf("Dismissed: %s", reason)
	}

	params := domain.CloseIssueParams{Reason: closeReason}
	if isWisp {
		_, err = uw.IssueUseCase().CloseWisp(ctx, resolvedID, params, actor)
	} else {
		_, err = uw.IssueUseCase().CloseIssue(ctx, resolvedID, params, actor)
	}
	if err != nil {
		return HandleErrorRespectJSON("closing bead: %v", err)
	}

	if cerr := uw.Commit(ctx, "bd: human dismiss "+resolvedID); cerr != nil && !isDoltNothingToCommit(cerr) {
		return HandleErrorRespectJSON("commit dismiss: %v", cerr)
	}

	fmt.Printf("%s Bead %s dismissed.\n", ui.RenderPass("✔"), resolvedID)
	return nil
}

// runHumanRespondProxiedServer records a response comment on a human-needed
// bead and closes it, through the proxied UOW (beads-ivje respond leg). It
// mirrors the direct-path respond handler: refuse an already-closed issue,
// warn (non-fatally) if the bead lacks the "human" label, add a
// "Response: <text>" comment, then close with a "Responded" reason. The
// comment step uses CommentUseCase.AddComment (added in beads-m4vx), which is
// what previously blocked this leg (the use-case was read-only).
func runHumanRespondProxiedServer(ctx context.Context, issueID, response string) error {
	provider, err := ensureProxiedUOWProvider(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	uw, err := provider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	issue, isWisp := proxiedResolveIssueOrWisp(ctx, uw, issueID)
	if issue == nil {
		return HandleErrorRespectJSON("issue not found: %s", issueID)
	}
	resolvedID := issue.ID

	if issue.Status == types.StatusClosed {
		return HandleErrorRespectJSON("issue %s is already closed", resolvedID)
	}

	if labels, lerr := uw.LabelUseCase().GetLabels(ctx, resolvedID); lerr == nil {
		hasHumanLabel := false
		for _, label := range labels {
			if label == "human" {
				hasHumanLabel = true
				break
			}
		}
		if !hasHumanLabel {
			fmt.Fprintf(os.Stderr, "Warning: Issue %s does not have 'human' label\n", resolvedID)
		}
	}

	commentText := fmt.Sprintf("Response: %s", response)
	if _, aerr := uw.CommentUseCase().AddComment(ctx, resolvedID, actor, commentText); aerr != nil {
		return HandleErrorRespectJSON("adding comment: %v", aerr)
	}

	params := domain.CloseIssueParams{Reason: "Responded"}
	if isWisp {
		_, err = uw.IssueUseCase().CloseWisp(ctx, resolvedID, params, actor)
	} else {
		_, err = uw.IssueUseCase().CloseIssue(ctx, resolvedID, params, actor)
	}
	if err != nil {
		return HandleErrorRespectJSON("closing bead: %v", err)
	}

	if cerr := uw.Commit(ctx, "bd: human respond "+resolvedID); cerr != nil && !isDoltNothingToCommit(cerr) {
		return HandleErrorRespectJSON("commit respond: %v", cerr)
	}

	fmt.Printf("%s Bead %s closed with response.\n", ui.RenderPass("✔"), resolvedID)
	return nil
}
