package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/ui"
)

// runCommentProxiedServer adds a comment to an issue/wisp via the proxied
// unit-of-work stack, for hub-connected crew where the global `store` is nil
// (beads-xjtk). This is the `bd comment` (singular shorthand) sibling of
// `bd comments add` (beads-m4vx): the direct path calls
// resolveAndGetIssueForMutation(nil store) → result.Store.AddIssueComment, so a
// proxied crew hit "storage is nil". It reuses CommentUseCase.AddComment (added
// by m4vx). Text resolution and the empty-text guard already happened in the
// caller; this mirrors the singular direct path's extra steps
// (validateIssueUpdatable + SetLastTouchedID + title-formatted confirmation).
func runCommentProxiedServer(ctx context.Context, issueID, author, commentText string) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	// Resolve issue or wisp so a nonexistent ID errors distinctly, matching the
	// direct path's resolveAndGetIssueForMutation.
	issue, _ := proxiedResolveIssueOrWisp(ctx, uw, issueID)
	if issue == nil {
		return HandleErrorRespectJSON("issue %s not found", issueID)
	}
	resolvedID := issue.ID

	// Refuse comments on templates, mirroring the direct path.
	if verr := validateIssueUpdatable(resolvedID, issue); verr != nil {
		return HandleErrorRespectJSON("%s", verr)
	}

	comment, err := uw.CommentUseCase().AddComment(ctx, resolvedID, author, commentText)
	if err != nil {
		return HandleErrorRespectJSON("adding comment: %v", err)
	}

	if cerr := uw.Commit(ctx, "bd: comment "+resolvedID); cerr != nil && !isDoltNothingToCommit(cerr) {
		return HandleErrorRespectJSON("failed to commit: %v", cerr)
	}

	SetLastTouchedID(resolvedID)

	if jsonOutput {
		return outputJSON(comment)
	}
	fmt.Printf("%s Comment added to %s\n", ui.RenderPass("✓"), formatFeedbackID(resolvedID, issue.Title))
	return nil
}
