package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/timeparsing"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var deferCmd = &cobra.Command{
	Use:   "defer [id...]",
	Short: "Defer one or more issues for later",
	Long: `Defer issues to put them on ice for later.

Deferred issues are deliberately set aside - not blocked by anything specific,
just postponed for future consideration. Unlike blocked issues, there's no
dependency keeping them from being worked. Unlike closed issues, they will
be revisited.

Deferred issues don't show in 'bd ready' but remain visible in 'bd list'.

Examples:
  bd defer bd-abc                  # Defer a single issue (status-based)
  bd defer bd-abc --until=tomorrow # Defer until specific time
  bd defer bd-abc --reason="waiting on API access"
  bd defer bd-abc bd-def           # Defer multiple issues`,
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("defer")

		evt := metrics.NewCommandEvent("defer")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		var deferUntil *time.Time
		untilStr, _ := cmd.Flags().GetString("until")
		if untilStr != "" {
			t, err := timeparsing.ParseRelativeTime(untilStr, time.Now())
			if err != nil {
				// bd defer supports --json; respect it on flag-validation
				// errors too, matching the ID-resolve path below (beads-xwjg).
				return HandleErrorRespectJSON("invalid --until format %q. Examples: +1h, tomorrow, next monday, 2025-01-15", untilStr)
			}
			if t.Before(time.Now()) && !jsonOutput {
				fmt.Fprintf(os.Stderr, "%s Defer date %q is in the past. Issue will appear in bd ready immediately.\n",
					ui.RenderWarn("!"), t.Format("2006-01-02 15:04"))
				fmt.Fprintf(os.Stderr, "  Did you mean a future date? Use --until=+1h or --until=tomorrow\n")
			}
			deferUntil = &t
		}
		reason, _ := cmd.Flags().GetString("reason")
		reason = strings.TrimSpace(reason)
		if cmd.Flags().Changed("reason") && reason == "" {
			return HandleErrorRespectJSON("reason cannot be empty")
		}

		ctx := rootCtx

		_, err := utils.ResolvePartialIDs(ctx, store, args)
		if err != nil {
			// Respect --json: an unresolvable ID must emit a stdout JSON error
			// object (beads-0l4c), not plain text to stderr, so --json consumers
			// can parse the failure instead of seeing empty stdout + exit 1.
			return HandleErrorRespectJSON("%v", err)
		}

		deferredIssues := []*types.Issue{}
		deferredCount := 0

		if store == nil {
			return HandleErrorWithHint("database not initialized", diagHint())
		}

		for _, id := range args {
			fullID, err := utils.ResolvePartialID(ctx, store, id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving %s: %v\n", id, err)
				continue
			}

			updates := map[string]interface{}{
				"status": string(types.StatusDeferred),
			}
			if deferUntil != nil {
				updates["defer_until"] = *deferUntil
			}
			// Load the issue up front for the pre-change status (audit trail) and
			// for appending the reason to notes. Fall back to "open" if the load
			// fails but the update later succeeds, matching close.go's default.
			oldStatus := "open"
			if issue, gerr := store.GetIssue(ctx, fullID); gerr == nil && issue != nil {
				oldStatus = string(issue.Status)
				if reason != "" {
					notes := issue.Notes
					if notes != "" {
						notes += "\n"
					}
					updates["notes"] = notes + reason
				}
			} else if reason != "" {
				// Reason requested but the issue couldn't be loaded: fail rather
				// than silently drop the reason (prior behavior).
				if gerr != nil {
					fmt.Fprintf(os.Stderr, "Error loading %s: %v\n", fullID, gerr)
				} else {
					fmt.Fprintf(os.Stderr, "Issue %s not found\n", fullID)
				}
				continue
			}

			if err := store.UpdateIssue(ctx, fullID, updates, actor); err != nil {
				fmt.Fprintf(os.Stderr, "Error deferring %s: %v\n", fullID, err)
				continue
			}
			// Audit log the defer status change (survives Dolt GC flatten) via
			// the shared cmd-layer chokepoint (beads-n4sn).
			auditStatusChange(fullID, oldStatus, string(types.StatusDeferred), actor, reason)
			deferredCount++

			if jsonOutput {
				issue, _ := store.GetIssue(ctx, fullID)
				if issue != nil {
					deferredIssues = append(deferredIssues, issue)
				}
			} else {
				fmt.Printf("%s Deferred %s\n", ui.RenderAccent("*"), fullID)
			}
		}

		if jsonOutput && len(deferredIssues) > 0 {
			if err := outputJSON(deferredIssues); err != nil {
				return err
			}
		}

		if deferredCount > 0 {
			commandDidWrite.Store(true)
		}

		// Every requested ID failed (per-item errors already printed to
		// stderr): exit non-zero so callers/scripts don't read false success.
		// Under --json, stdout is still empty here, so emit a stdout JSON error
		// object to keep the failure parseable (beads-fg6, matching the update
		// and close batch paths) instead of a bare silent exit. Partial success
		// (deferredCount>0) keeps rc=0 and its JSON array above, mirroring
		// update/close batch semantics.
		if len(args) > 0 && deferredCount == 0 {
			if jsonOutput {
				return HandleErrorRespectJSON("no issues deferred matching the provided IDs")
			}
			return SilentExit()
		}
		return nil
	},
}

func init() {
	// Time-based scheduling flag (GH#820)
	deferCmd.Flags().String("until", "", "Defer until specific time (e.g., +1h, tomorrow, next monday)")
	deferCmd.Flags().String("reason", "", "Record why this issue is being deferred (appended to notes)")
	deferCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(deferCmd)
}
