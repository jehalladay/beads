package main

import (
	"context"
	"fmt"
)

// runCommentsAddProxiedServer adds a comment to an issue/wisp via the proxied
// unit-of-work stack, for hub-connected crew where the global `store` is nil
// (beads-m4vx, fszd/aocj umbrella). The direct path uses
// result.Store.AddIssueComment; CommentUseCase was read-only, so this is an
// interface-extension leg: AddComment was added to CommentUseCase (backed by
// issueops.AddIssueCommentInTx widened *sql.Tx→DBTX). Text/author resolution and
// the empty-text guard happened in the caller. Mirrors cmd/bd/comments.go.
func runCommentsAddProxiedServer(ctx context.Context, issueID, author, commentText string) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	// beads-mrz0u: resolve bare-hash/partial IDs via the shared helper (beads-3ii21)
	// so a hub-connected crew's `bd comments add <partial>` works like the direct
	// path (comments.go routing → resolveAndGetIssueForMutation), then rebind
	// issueID to the canonical ID for AddComment/Commit. Verifies existence (issue
	// or wisp) so a nonexistent ID errors distinctly.
	resolved, _, gerr := proxiedGetIssueOrWisp(ctx, uw, issueID)
	if gerr != nil {
		return HandleErrorRespectJSON("resolving %s: %v", issueID, gerr)
	}
	if resolved == nil {
		return HandleErrorRespectJSON("issue %s not found", issueID)
	}
	issueID = resolved.ID

	comment, err := uw.CommentUseCase().AddComment(ctx, issueID, author, commentText)
	if err != nil {
		return HandleErrorRespectJSON("adding comment: %v", err)
	}

	// beads-29tyj: capture the post-add snapshot before Commit so the on_update
	// hook can fire after — the direct path fires on_update via
	// HookFiringStore.AddIssueComment, but the proxied UOW use-case layer does not.
	after := captureProxiedHookSnapshot(ctx, uw, issueID, false)

	if err := uw.Commit(ctx, fmt.Sprintf("bd: comment %s", issueID)); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	// beads-29tyj: fire on_update after the commit (parity with the direct decorator).
	fireProxiedUpdateSnapshots(ctx, after)

	if jsonOutput {
		return outputJSON(comment)
	}
	fmt.Printf("Comment added to %s\n", issueID)
	return nil
}
