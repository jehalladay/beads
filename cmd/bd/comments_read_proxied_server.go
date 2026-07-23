package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/uimd"
)

// runCommentsListProxiedServer lists an issue's/wisp's comments via the proxied
// unit-of-work stack, for hub-connected crew where the global `store` is nil
// (beads-f2vu, fszd/aocj read leg). The direct path uses
// result.Store.GetIssueComments; this is a CLEAN-MIRROR read (no interface
// extension) since CommentUseCase.GetCommentsForIssue/GetCommentsForWisp already
// exist on the UOW. Mirrors the SHOW parent in cmd/bd/comments.go.
func runCommentsListProxiedServer(ctx context.Context, issueID string, localTime bool) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	// beads-mrz0u: resolve bare-hash/partial IDs via the shared helper (beads-3ii21)
	// so a hub-connected crew's `bd comments <partial>` works like the direct path,
	// then rebind issueID to the canonical ID. Resolves issue vs wisp so comments
	// come from the right table, and so a nonexistent ID errors like the direct
	// path rather than silently returning an empty list.
	resolved, isWisp, gerr := proxiedGetIssueOrWisp(ctx, uw, issueID)
	if gerr != nil {
		return HandleErrorRespectJSON("resolving %s: %v", issueID, gerr)
	}
	if resolved == nil {
		return HandleErrorRespectJSON("issue %s not found", issueID)
	}
	issueID = resolved.ID

	var comments []*types.Comment
	if isWisp {
		comments, err = uw.CommentUseCase().GetCommentsForWisp(ctx, issueID)
	} else {
		comments, err = uw.CommentUseCase().GetCommentsForIssue(ctx, issueID)
	}
	if err != nil {
		return HandleErrorRespectJSON("getting comments: %v", err)
	}
	if comments == nil {
		comments = make([]*types.Comment, 0)
	}

	if jsonOutput {
		return outputJSON(comments)
	}

	if len(comments) == 0 {
		fmt.Printf("No comments on %s\n", issueID)
		return nil
	}

	fmt.Printf("\nComments on %s:\n\n", issueID)
	for _, comment := range comments {
		ts := comment.CreatedAt
		if localTime {
			ts = ts.Local()
		}
		fmt.Printf("[%s] at %s\n", ui.SanitizeForTerminal(comment.Author), ts.Format("2006-01-02 15:04"))
		rendered := uimd.RenderMarkdown(comment.Text)
		for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
			fmt.Printf("  %s\n", line)
		}
		fmt.Println()
	}
	return nil
}
