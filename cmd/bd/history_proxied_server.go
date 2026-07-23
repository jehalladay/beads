package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/ui"
)

// runHistoryProxiedServer shows an issue's (or wisp's) version history via the
// proxied unit-of-work stack, for hub-connected crew where the global `store`
// is nil (beads-t3wg, fszd/aocj umbrella). It mirrors the direct path
// (cmd/bd/history.go): verify the issue exists first (so a nonexistent ID exits
// non-zero like show/comments — rather than being indistinguishable from an
// existing issue with no history, beads-4skk), fetch History() from the UOW
// IssueUseCase, apply the --limit truncation, and honor --json + the same
// human-readable rendering. --limit was already validated non-negative by the
// caller (validateLimitFromCmd).
func runHistoryProxiedServer(ctx context.Context, issueID string) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()

	// beads-mrz0u: resolve bare-hash/partial IDs via the shared helper (beads-3ii21)
	// so a hub-connected crew's `bd history <partial>` works like the direct path,
	// then rebind issueID to the canonical ID for History(). Verifies existence
	// (issue or wisp) so a nonexistent ID errors rc!=0 like show/comments/children.
	issue, _, gerr := proxiedGetIssueOrWisp(ctx, uw, issueID)
	if gerr != nil {
		return HandleErrorRespectJSON("resolving %s: %v", issueID, gerr)
	}
	if issue == nil {
		return HandleErrorRespectJSON("issue %s not found", issueID)
	}
	issueID = issue.ID

	history, err := issueUC.History(ctx, issueID)
	if err != nil {
		return HandleErrorRespectJSON("failed to get history: %v", err)
	}

	if len(history) == 0 {
		if jsonOutput {
			// beads-5983i: normalize nil→[] so the proxied path emits the array
			// contract on empty (matches the direct path history.go); a nil
			// []*HistoryEntry would marshal as JSON null.
			return outputJSON([]*storage.HistoryEntry{})
		}
		fmt.Printf("No history found for issue %s\n", issueID)
		return nil
	}

	// Capture the true total before truncation so the header does not
	// misreport the --limit page size as the entry count (beads-qal3,
	// symmetric with the direct path).
	totalEntries := len(history)
	truncated := false
	if historyLimit > 0 && historyLimit < len(history) {
		history = history[:historyLimit]
		truncated = true
	}

	if jsonOutput {
		return outputJSON(history)
	}

	if truncated {
		fmt.Printf("\n%s History for %s (showing %d of %d entries)\n\n",
			ui.RenderAccent("📜"), issueID, len(history), totalEntries)
	} else {
		fmt.Printf("\n%s History for %s (%d entries)\n\n",
			ui.RenderAccent("📜"), issueID, totalEntries)
	}

	for i, entry := range history {
		fmt.Printf("%s %s\n",
			ui.RenderMuted(entry.CommitHash[:8]),
			ui.RenderMuted(entry.CommitDate.Format("2006-01-02 15:04:05")))
		// Match the direct-path human view + --json "committer" field: this is
		// the Dolt committer, not the issue author (beads-lf39).
		fmt.Printf("  Committer: %s\n", entry.Committer)

		if entry.Issue != nil {
			// beads-f956y: route through the shared sanitizing helper (in
			// history.go) so the proxied twin can't drift from the direct view.
			fmt.Println(formatHistoryIssueLine(entry))
		}

		if i < len(history)-1 {
			fmt.Println()
		}
	}
	fmt.Println()
	return nil
}
