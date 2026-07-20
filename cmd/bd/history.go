package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/ui"
)

var (
	historyLimit int
)

var historyCmd = &cobra.Command{
	Use:     "history <id>",
	GroupID: "views",
	Short:   "Show version history for an issue",
	Long: `Show the complete version history of an issue, including all commits
where the issue was modified.

Examples:
  bd history bd-123           # Show all history for issue bd-123
  bd history bd-123 --limit 5 # Show last 5 changes`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("history")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		// Reject a negative --limit up front (beads-4djp): the shared
		// negative-limit contract (eqi4/r9hj). The truncation at history.go:72
		// only fires when historyLimit > 0, so a negative value silently returns
		// the FULL history with rc=0 — a misleading false-green. --limit 0
		// (documented "all") and positives are unchanged.
		if err := validateLimitFromCmd(cmd); err != nil {
			return err
		}

		ctx := rootCtx
		issueID := args[0]

		// In proxied-server mode the global `store` is nil (main.go
		// PersistentPreRun returns before newDoltStore), so the
		// resolveAndGetIssueWithRouting(store, ...) path below would fail with
		// "storage is nil". Route History() through the proxied UOW stack
		// instead (beads-t3wg, fszd/aocj umbrella) — History was previously only
		// on DoltStore, now also on the domain IssueUseCase.
		if usesProxiedServer() {
			return runHistoryProxiedServer(ctx, issueID)
		}

		// Verify the issue exists first (with prefix routing), so a nonexistent
		// ID errors rc!=0 like show/comments/children — rather than being
		// indistinguishable from an existing issue that simply has no history
		// (beads-4skk). This also routes History() to the owning rig's store.
		result, err := resolveAndGetIssueWithRouting(ctx, store, issueID)
		if err != nil {
			if result != nil {
				result.Close()
			}
			return HandleErrorRespectJSON("resolving %s: %v", issueID, err)
		}
		if result == nil || result.Issue == nil {
			if result != nil {
				result.Close()
			}
			return HandleErrorRespectJSON("issue %s not found", issueID)
		}
		defer result.Close()
		issueID = result.ResolvedID

		history, err := result.Store.History(ctx, issueID)
		if err != nil {
			return HandleErrorRespectJSON("failed to get history: %v", err)
		}

		if len(history) == 0 {
			if jsonOutput {
				// beads-5983i: History() returns a nil []*HistoryEntry on the
				// empty path, which wrapWithSchemaVersion emits as JSON `null`
				// (nil still satisfies reflect.Slice → returned as-is). Normalize
				// to [] so `bd history --json` honors the array contract on empty,
				// matching every other --json list path (guib/tamf/036h class).
				return outputJSON([]*storage.HistoryEntry{})
			}
			fmt.Printf("No history found for issue %s\n", issueID)
			return nil
		}

		// Capture the true total before truncation so the header does not
		// misreport the --limit page size as the entry count (beads-qal3,
		// sibling of the 48g6/phmp/4wn0/ebpo/l39v limit-truncation class).
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
			fmt.Printf("  Author: %s\n", entry.Committer)

			if entry.Issue != nil {
				statusIcon := ui.GetStatusIcon(string(entry.Issue.Status))
				fmt.Printf("  %s %s: %s [P%d - %s]\n",
					statusIcon,
					entry.Issue.ID,
					entry.Issue.Title,
					entry.Issue.Priority,
					entry.Issue.Status)
			}

			if i < len(history)-1 {
				fmt.Println()
			}
		}
		fmt.Println()
		return nil
	},
}

func init() {
	historyCmd.Flags().IntVar(&historyLimit, "limit", 0, "Limit number of history entries (0 = all)")
	historyCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(historyCmd)
}
