package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

var reopenCmd = &cobra.Command{
	Use:     "reopen [id...]",
	GroupID: "issues",
	Short:   "Reopen one or more closed issues",
	Long: `Reopen closed issues by setting status to 'open' and clearing the closed_at timestamp.
This is more explicit than 'bd update --status open' and emits a Reopened event.`,
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("reopen")

		evt := metrics.NewCommandEvent("reopen")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if usesProxiedServer() {
			runReopenProxiedServer(cmd, rootCtx, args)
			return nil
		}

		reason, _ := cmd.Flags().GetString("reason")
		forceFlag, _ := cmd.Flags().GetBool("force")
		ctx := rootCtx

		reopenedIssues := []*types.Issue{}
		hasError := false
		mutatedStores := map[storage.DoltStorage][]string{}
		pendingCloseResults := []*RoutedResult{}
		if store == nil {
			return HandleErrorWithHint("database not initialized", diagHint())
		}
		// beads-en28: under --json these per-item messages must NOT interleave
		// plain text onto the stream a `2>&1` consumer parses. On a wholly-failed
		// batch the terminal HandleErrorRespectJSON stdout object is the sole
		// error, so stderr must stay clean; on PARTIAL success the found array is
		// on stdout, so per-item failures flush to stderr as JSON objects. Defer
		// them and decide at the end, mirroring show.go's reportShowItemError
		// (beads-fg6/beads-92tz). Non-JSON keeps immediate stderr (correct today).
		var deferredItemErrors []string
		reportReopenItemError := func(format string, a ...interface{}) {
			if jsonOutput {
				deferredItemErrors = append(deferredItemErrors, fmt.Sprintf(format, a...))
				return
			}
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		}
		for _, id := range args {
			// Resolve with prefix routing (supports cross-rig reopens like `bd reopen xe-5ls`)
			result, err := resolveAndGetIssueForMutation(ctx, store, id)
			if err != nil {
				reportReopenItemError("Error resolving %s: %v", id, err)
				hasError = true
				continue
			}
			fullID := result.ResolvedID
			issueStore := result.Store
			issue := result.Issue

			// reopen only applies to closed issues (see command help). Guard
			// every non-closed status, not just already-open: reopening an
			// in_progress/blocked/deferred bead would silently revert it to
			// open and emit a misleading "Reopened" event for work that was
			// never closed. Treat all non-closed states as a no-op with a
			// clear message (matching the long-standing already-open behavior).
			if issue.Status != types.StatusClosed {
				if issue.Status == types.StatusOpen {
					// beads-hxc2: an already-open reopen is an idempotent no-op
					// SUCCESS — the issue is already in reopen's target state, so
					// exit stays 0. Reflect that state in the --json payload
					// (mirroring close.go's already-closed path, which adds the
					// issue to the "closed" array) rather than emitting an
					// {"error":...}-keyed object on stderr for a non-error
					// outcome, which mislabels the success and is asymmetric with
					// close. Non-JSON keeps the informational stderr line.
					if jsonOutput {
						reopenedIssues = append(reopenedIssues, issue)
					} else {
						fmt.Fprintf(os.Stderr, "%s is already open\n", fullID)
					}
				} else {
					// A non-closed, non-open status (deferred/in_progress/blocked):
					// reopen deliberately does not apply here (it would silently
					// revert real work to open). This is an advisory no-op, not a
					// reflected target state, so it stays a deferred per-item
					// message (JSON object on stderr under --json / plain line
					// otherwise), distinct from the already-open success above.
					reportReopenItemError("%s is not closed (status: %s); reopen only applies to closed issues", fullID, issue.Status)
				}
				result.Close()
				continue
			}
			// Closed-epic-parent guard (beads-b0tw): reopening a closed child
			// whose parent epic is itself closed silently recreates the
			// closed-epic-with-open-child inconsistency the close-guard family
			// prevents ("a closed epic has no open children" is enforced only at
			// epic-close, not at child-reopen). Refuse unless --force, mirroring
			// `bd close --force`. This is a real closed->open transition (the
			// guard above returned for every non-closed status).
			if !forceFlag {
				if closedEpics := closedEpicParents(ctx, issueStore, fullID); len(closedEpics) > 0 {
					reportReopenItemError("cannot reopen %s: its parent epic %v is closed; reopen the epic first or use --force to override", fullID, closedEpics)
					hasError = true
					result.Close()
					continue
				}
			}

			if err := issueStore.ReopenIssue(ctx, fullID, reason, actor); err != nil {
				reportReopenItemError("Error reopening %s: %v", fullID, err)
				hasError = true
				result.Close()
				continue
			}
			mutatedStores[issueStore] = append(mutatedStores[issueStore], fullID)
			pendingCloseResults = append(pendingCloseResults, result)

			// Audit log the reopen (survives Dolt GC flatten) via the shared
			// cmd-layer chokepoint. Without this, a GC flatten would leave the
			// durable trail showing the close but not the reopen (beads-n4sn).
			// The guard above guarantees this is a real closed->open transition.
			auditStatusChange(fullID, string(issue.Status), string(types.StatusOpen), actor, reason)
			if jsonOutput {
				updated, _ := issueStore.GetIssue(ctx, fullID)
				if updated != nil {
					reopenedIssues = append(reopenedIssues, updated)
				}
			} else {
				reasonMsg := ""
				if reason != "" {
					reasonMsg = ": " + reason
				}
				fmt.Printf("%s Reopened %s%s\n", ui.RenderAccent("↻"), fullID, reasonMsg)
			}
		}

		for s, ids := range mutatedStores {
			if err := commitPendingIfEmbedded(ctx, s, actor, doltAutoCommitParams{
				Command:  "reopen",
				IssueIDs: ids,
			}); err != nil {
				for _, result := range pendingCloseResults {
					result.Close()
				}
				return HandleErrorRespectJSON("failed to commit: %v", err)
			}
		}
		for _, result := range pendingCloseResults {
			result.Close()
		}

		if jsonOutput && len(reopenedIssues) > 0 {
			// Partial success: stdout carries the reopened array, so any deferred
			// per-item failures flush to stderr as JSON objects (beads-en28/fg6).
			for _, msg := range deferredItemErrors {
				reportItemError("%s", msg)
			}
			if jerr := outputJSON(reopenedIssues); jerr != nil {
				return jerr
			}
		}

		if hasError {
			// If nothing was reopened at all, the batch wholly failed. Under
			// --json, stdout is still empty here, so emit a stdout JSON error
			// object to keep the failure parseable (beads-fg6/beads-tx70,
			// matching the update and close batch paths) instead of a bare
			// SilentExit that leaves --json consumers with empty stdout. When
			// SOME issues reopened (partial success), their JSON array was
			// already emitted above, so keep the plain non-zero exit.
			if jsonOutput && len(reopenedIssues) == 0 {
				return HandleErrorRespectJSON("no issues reopened matching the provided IDs")
			}
			return SilentExit()
		}
		// No-op-only --json path (e.g. every id was already open / not closed, so
		// hasError stayed false and nothing was reopened): stdout is empty, so
		// flush the deferred status messages to stderr as JSON objects rather than
		// dropping them — keeps the info visible without interleaving plain text
		// onto a 2>&1 stream (beads-en28).
		if jsonOutput && len(reopenedIssues) == 0 {
			for _, msg := range deferredItemErrors {
				reportItemError("%s", msg)
			}
		}
		return nil
	},
}

func init() {
	reopenCmd.Flags().StringP("reason", "r", "", "Reason for reopening")
	reopenCmd.Flags().Bool("force", false, "Override the closed-epic-parent guard (reopening a child whose parent epic is closed); recreates a closed-epic-with-open-child state")
	reopenCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(reopenCmd)
}
