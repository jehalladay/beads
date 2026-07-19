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

		// beads-bqs9: under --json these per-item messages must NOT interleave
		// plain text onto the stream a `2>&1` consumer parses. On a wholly-failed
		// batch the terminal HandleErrorRespectJSON stdout object below is the
		// sole error, so stderr must stay clean; on PARTIAL success the
		// undeferred array is on stdout, so per-item failures flush to stderr as
		// JSON objects. Defer them and decide at the end, mirroring show.go's
		// reportShowItemError / beads-en28 / beads-n96g. Non-JSON keeps immediate
		// stderr (correct today).
		var deferredItemErrors []string
		reportUndeferItemError := func(format string, a ...interface{}) {
			if jsonOutput {
				deferredItemErrors = append(deferredItemErrors, fmt.Sprintf(format, a...))
				return
			}
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		}

		for _, id := range args {
			fullID, err := utils.ResolvePartialID(ctx, store, id)
			if err != nil {
				reportUndeferItemError("Error resolving %s: %v", id, err)
				continue
			}

			issue, err := store.GetIssue(ctx, fullID)
			if err != nil {
				reportUndeferItemError("Error getting %s: %v", fullID, err)
				continue
			}
			if issue.Status != types.StatusDeferred {
				reportUndeferItemError("%s is not deferred (status: %s)", fullID, string(issue.Status))
				continue
			}

			updates := map[string]interface{}{
				"status":      string(types.StatusOpen),
				"defer_until": nil,
			}

			if err := store.UpdateIssue(ctx, fullID, updates, actor); err != nil {
				reportUndeferItemError("Error undeferring %s: %v", fullID, err)
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
			// Partial success: stdout carries the undeferred array, so any
			// deferred per-item failures flush to stderr as JSON objects now
			// rather than being dropped (beads-bqs9/en28). On a wholly-failed
			// batch undeferredIssues is empty, so we skip this and the terminal
			// HandleErrorRespectJSON below is the sole error (stderr stays clean).
			// reportItemError (errors.go:131) JSON-wraps each message to stderr
			// under --json.
			for _, msg := range deferredItemErrors {
				reportItemError("%s", msg)
			}
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
