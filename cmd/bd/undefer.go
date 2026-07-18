package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var undeferCmd = &cobra.Command{
	Use:   "undefer [id...]",
	Short: "Undefer one or more issues (restore to open)",
	Long: `Undefer issues to restore them to open status.

This brings issues back from the icebox so they can be worked on again.
Issues will appear in 'bd ready' if they have no blockers.

Examples:
  bd undefer bd-abc        # Undefer a single issue
  bd undefer bd-abc bd-def # Undefer multiple issues`,
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("undefer")

		// beads-fszd/1zuh: route to the proxied handler in proxied-server mode.
		// Without this, undefer uses the direct global store — nil under
		// proxiedServerMode — and returned "database not initialized" for
		// hub-connected crew instead of undeferring.
		if usesProxiedServer() {
			return runUndeferProxiedServer(rootCtx, args)
		}

		evt := metrics.NewCommandEvent("undefer")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		ctx := rootCtx

		_, err := utils.ResolvePartialIDs(ctx, store, args)
		if err != nil {
			// Respect --json: an unresolvable ID must emit a stdout JSON error
			// object (beads-7pcm), not plain text to stderr, so --json consumers
			// can parse the failure instead of seeing empty stdout + exit 1.
			return HandleErrorRespectJSON("%v", err)
		}

		undeferredIssues := []*types.Issue{}
		undeferredCount := 0

		if store == nil {
			return HandleErrorWithHint("database not initialized", diagHint())
		}

		for _, id := range args {
			fullID, err := utils.ResolvePartialID(ctx, store, id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving %s: %v\n", id, err)
				continue
			}

			issue, err := store.GetIssue(ctx, fullID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting %s: %v\n", fullID, err)
				continue
			}
			if issue.Status != types.StatusDeferred {
				fmt.Fprintf(os.Stderr, "%s is not deferred (status: %s)\n", fullID, string(issue.Status))
				continue
			}

			updates := map[string]interface{}{
				"status":      string(types.StatusOpen),
				"defer_until": nil,
			}

			if err := store.UpdateIssue(ctx, fullID, updates, actor); err != nil {
				fmt.Fprintf(os.Stderr, "Error undeferring %s: %v\n", fullID, err)
				continue
			}
			undeferredCount++
			// GC-survivable audit trail via the shared chokepoint: undefer is a
			// deferred->open status transition, same class as reopen/defer
			// (beads-n4sn). issue is guaranteed deferred by the guard above.
			auditStatusChange(fullID, string(issue.Status), string(types.StatusOpen), actor, "")

			if jsonOutput {
				issue, _ := store.GetIssue(ctx, fullID)
				if issue != nil {
					undeferredIssues = append(undeferredIssues, issue)
				}
			} else {
				fmt.Printf("%s Undeferred %s (now open)\n", ui.RenderPass("*"), fullID)
			}
		}

		if jsonOutput && len(undeferredIssues) > 0 {
			if err := outputJSON(undeferredIssues); err != nil {
				return err
			}
		}

		if undeferredCount > 0 {
			commandDidWrite.Store(true)
		}

		// Every requested ID failed (per-item errors already printed to
		// stderr): exit non-zero so callers/scripts don't read false success.
		// Under --json, stdout is still empty here, so emit a stdout JSON error
		// object to keep the failure parseable (beads-7pcm, mirroring the
		// deferred/update/close batch paths). Partial success
		// (undeferredCount>0) keeps rc=0 and its JSON array above.
		if len(args) > 0 && undeferredCount == 0 {
			if jsonOutput {
				return HandleErrorRespectJSON("no issues undeferred matching the provided IDs")
			}
			return SilentExit()
		}

		return nil
	},
}

func init() {
	undeferCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(undeferCmd)
}
