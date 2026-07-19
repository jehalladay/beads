package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steveyegge/beads/internal/ui"
)

// runPromoteProxiedServer promotes a wisp to a permanent bead via the proxied
// unit-of-work stack, for hub-connected crew where the global `store` is nil
// (beads-aocj promote leg). The direct path (cmd/bd/promote.go) calls
// store.GetIssue / store.PromoteFromEphemeral / store.AddComment, so a proxied
// crew hit "database not initialized". It reuses the UOW IssueUseCase
// (PromoteFromEphemeral, added alongside the existing RenameIssueID delegate)
// and CommentUseCase (AddComment, beads-m4vx), committing both the promotion
// and the recording comment in one unit of work.
func runPromoteProxiedServer(ctx context.Context, id, reason string) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	// Resolve wisp-first, mirroring the direct path's semantics: promote targets
	// the wisps table. A wisp-only id would not resolve via the issues-first
	// proxiedResolveIssueOrWisp, and a matching permanent issue must be reported
	// as "not a wisp" rather than promoted.
	wisp, werr := uw.IssueUseCase().GetWisp(ctx, id)
	if werr != nil || wisp == nil {
		// Distinguish "already permanent" (a plain issue exists) from truly
		// missing, matching the direct path's not-a-wisp vs not-found errors.
		if issue, ierr := uw.IssueUseCase().GetIssue(ctx, id); ierr == nil && issue != nil {
			return HandleErrorRespectJSON("%s is not a wisp (already persistent)", issue.ID)
		}
		return HandleErrorRespectJSON("issue %s not found", id)
	}
	resolvedID := wisp.ID

	if !wisp.Ephemeral {
		return HandleErrorRespectJSON("%s is not a wisp (already persistent)", resolvedID)
	}

	if err := uw.IssueUseCase().PromoteFromEphemeral(ctx, resolvedID, actor); err != nil {
		return HandleErrorRespectJSON("promoting %s: %v", resolvedID, err)
	}

	// The row now lives in the permanent issues table; record the promotion
	// comment there. A comment failure is non-fatal, matching the direct path.
	comment := "Promoted from wisp to permanent bead"
	if reason != "" {
		comment += ": " + reason
	}
	if _, cerr := uw.CommentUseCase().AddComment(ctx, resolvedID, actor, comment); cerr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to add promotion comment to %s: %v\n", resolvedID, cerr)
	}

	if cerr := uw.Commit(ctx, "bd: promote "+resolvedID); cerr != nil && !isDoltNothingToCommit(cerr) {
		return HandleErrorRespectJSON("failed to commit: %v", cerr)
	}

	SetLastTouchedID(resolvedID)

	if jsonOutput {
		updated, _ := uw.IssueUseCase().GetIssue(ctx, resolvedID)
		if updated != nil {
			return outputJSON(updated)
		}
		return nil
	}
	fmt.Printf("%s Promoted %s to permanent bead\n", ui.RenderPass("✓"), resolvedID)
	return nil
}
