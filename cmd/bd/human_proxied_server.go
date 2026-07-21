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
		if !hasHumanLabel && !jsonOutput {
			// beads-3xp23: guard behind !jsonOutput (stderr-warn-under-json class),
			// at parity with the direct dismiss leg.
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

	// beads-rbqo8 (CLOSE-PARITY-MATRIX, proxied dismiss leg): stage the molecule
	// auto-close cascade INTO this UOW before the commit, so a molecule whose final
	// open step is a human-gate bead closes at parity with bd close (which fires
	// autoCloseProxiedCompletedMolecule in close_proxied_server.go). Wisp targets
	// are not molecule steps, so only cascade the issue close. The returned root id
	// (if any) gets its GC-survivable audit-file trail AFTER uw.Commit succeeds
	// (jcrp4 ordering: audit.LogFieldChange writes a cwd file, not a UOW op).
	var autoClosedRoot string
	if !isWisp {
		autoClosedRoot = autoCloseProxiedCompletedMolecule(ctx, uw, resolvedID, actor, "", jsonOutput)
	}

	dismissOldStatus := string(issue.Status)
	if cerr := uw.Commit(ctx, "bd: human dismiss "+resolvedID); cerr != nil && !isDoltNothingToCommit(cerr) {
		return HandleErrorRespectJSON("commit dismiss: %v", cerr)
	}
	if autoClosedRoot != "" {
		auditStatusChange(autoClosedRoot, "open", "closed", actor, "all steps complete")
	}

	// beads-mw44m: write the GC-survivable audit-FILE trail (.beads/
	// interactions.jsonl) for the dismiss close, at parity with the direct leg
	// and bd close/gate-resolve/supersede (n4sn/r3m8v/1jkl5). audit.LogFieldChange
	// writes a cwd-based file, NOT a UOW op, so it must run AFTER uw.Commit
	// succeeds — a pre-commit emit would orphan the entry if the deferred uw.Close
	// rolled the tx back (r3m8v proxied precedent). The already-closed guard above
	// returned early, so reaching here is a real open→closed transition.
	auditStatusChange(resolvedID, dismissOldStatus, "closed", actor, closeReason)

	// beads-3xp23: honor --json on success at parity with the direct leg.
	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"id":     resolvedID,
			"status": "closed",
			"action": "dismissed",
			"reason": closeReason,
		})
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
		if !hasHumanLabel && !jsonOutput {
			// beads-3xp23: guard behind !jsonOutput (stderr-warn-under-json class),
			// at parity with the direct respond leg.
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

	// beads-rbqo8 (CLOSE-PARITY-MATRIX, proxied respond leg): stage the molecule
	// auto-close cascade into this UOW before commit so a molecule whose final open
	// step is a human-gate bead closes at parity with bd close. Wisps are not
	// molecule steps → issue-close only. Root audit-file trail emitted post-commit
	// (jcrp4 ordering).
	var autoClosedRoot string
	if !isWisp {
		autoClosedRoot = autoCloseProxiedCompletedMolecule(ctx, uw, resolvedID, actor, "", jsonOutput)
	}

	respondOldStatus := string(issue.Status)
	if cerr := uw.Commit(ctx, "bd: human respond "+resolvedID); cerr != nil && !isDoltNothingToCommit(cerr) {
		return HandleErrorRespectJSON("commit respond: %v", cerr)
	}
	if autoClosedRoot != "" {
		auditStatusChange(autoClosedRoot, "open", "closed", actor, "all steps complete")
	}

	// beads-mw44m: write the GC-survivable audit-FILE trail for the respond close
	// AFTER uw.Commit (parity with the direct leg + r3m8v proxied precedent; a
	// pre-commit emit would orphan the cwd-file entry on a tx rollback). The
	// already-closed guard above returned early → real open→closed transition.
	auditStatusChange(resolvedID, respondOldStatus, "closed", actor, "Responded")

	// beads-3xp23: honor --json on success at parity with the direct leg.
	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"id":     resolvedID,
			"status": "closed",
			"action": "responded",
			"reason": "Responded",
		})
	}

	fmt.Printf("%s Bead %s closed with response.\n", ui.RenderPass("✔"), resolvedID)
	return nil
}
