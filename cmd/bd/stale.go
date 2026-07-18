package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

var staleCmd = &cobra.Command{
	Use:     "stale",
	Args:    cobra.NoArgs,
	GroupID: "views",
	Short:   "Show stale issues (not updated recently)",
	Long: `Show issues that haven't been updated recently and may need attention.
This helps identify:
- In-progress issues with no recent activity (may be abandoned)
- Open issues that have been forgotten
- Issues that might be outdated or no longer relevant`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("stale")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		days, _ := cmd.Flags().GetInt("days")
		status, _ := cmd.Flags().GetString("status")
		limit, _ := cmd.Flags().GetInt("limit")
		if days < 1 {
			return HandleErrorRespectJSON("--days must be at least 1")
		}
		// beads-r9hj: route --limit through the shared chokepoint (beads-eqi4).
		// eqi4 guarded every sibling read command's --limit but missed stale:
		// GetStaleIssuesInTx emits a LIMIT clause only when filter.Limit > 0, so
		// a negative --limit is false → no LIMIT → the FULL result set with rc=0
		// (the same false-green eqi4 fixed for ready/search/query/gate/...).
		if err := validateLimitFromCmd(cmd); err != nil {
			return err
		}

		ctx := rootCtx

		// beads-de4r: validate --status via the shared custom-status-aware set
		// (bd list/count/search/lint/human list), NOT a hardcoded literal list.
		// The old guard hardcoded open/in_progress/blocked/deferred, which
		// OVER-rejected closed/pinned/hooked and any repo-configured custom
		// status — all valid Status values that bd list accepts and that the
		// storage layer (issueops/stale.go, a plain `status = ?` clause) handles
		// fine. 'all' means no status filter, matching the other read commands.
		if status == "all" {
			status = ""
		} else if status != "" {
			staleFilterCfg, cfgErr := loadDirectListFilterConfig(ctx, store)
			if cfgErr != nil {
				return HandleErrorRespectJSON("%v", cfgErr)
			}
			s := types.Status(status).Normalize()
			if !s.IsValidWithCustom(staleFilterCfg.customStatusNames()) {
				return HandleErrorRespectJSON("invalid status %q (valid: %s)", status, validStatusList(staleFilterCfg.customStatusNames()))
			}
			status = string(s)
		}
		// beads-phmp: --limit truncates the result set, and the human header read
		// len(issues) as if it were the true stale count — a plain `bd stale` on a
		// workspace with >50 stale issues printed "Stale issues (50 ...)" while more
		// existed. Fetch one extra row to detect truncation, then trim + qualify the
		// header, mirroring the `bd list` fetch-one-extra convention (list.go:43-44).
		effectiveLimit := limit
		if limit > 0 {
			filter := types.StaleFilter{Days: days, Status: status, Limit: limit + 1}
			issues, err := store.GetStaleIssues(ctx, filter)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			truncated := len(issues) > effectiveLimit
			if truncated {
				issues = issues[:effectiveLimit]
			}
			if jsonOutput {
				if issues == nil {
					issues = []*types.Issue{}
				}
				if err := outputJSON(issues); err != nil {
					return err
				}
				printTruncationHint(truncated, effectiveLimit)
				return nil
			}
			displayStaleIssues(issues, days, truncated, effectiveLimit)
			printTruncationHint(truncated, effectiveLimit)
			return nil
		}

		filter := types.StaleFilter{
			Days:   days,
			Status: status,
			Limit:  limit,
		}

		issues, err := store.GetStaleIssues(ctx, filter)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		if jsonOutput {
			if issues == nil {
				issues = []*types.Issue{}
			}
			return outputJSON(issues)
		}
		displayStaleIssues(issues, days, false, effectiveLimit)
		return nil
	},
}

func displayStaleIssues(issues []*types.Issue, days int, truncated bool, effectiveLimit int) {
	if len(issues) == 0 {
		fmt.Printf("\n%s No stale issues found (all active)\n\n", ui.RenderPass("✨"))
		return
	}
	if truncated {
		// Qualify the count so it is not read as a complete total (beads-phmp,
		// the l39v sibling). The exact overflow count needs a separate unlimited
		// COUNT query (a store change); the "+ more" hint plus the stderr
		// truncation notice make the partial view unmistakable without one.
		fmt.Printf("\n%s Showing %d stale issues (limit %d; not updated in %d+ days — more exist):\n\n",
			ui.RenderWarn("⏰"), len(issues), effectiveLimit, days)
	} else {
		fmt.Printf("\n%s Stale issues (%d not updated in %d+ days):\n\n", ui.RenderWarn("⏰"), len(issues), days)
	}
	now := time.Now()
	for i, issue := range issues {
		daysStale := int(now.Sub(issue.UpdatedAt).Hours() / 24)
		fmt.Printf("%d. [%s] %s: %s\n", i+1, ui.RenderPriority(issue.Priority), ui.RenderID(issue.ID), issue.Title)
		fmt.Printf("   Status: %s, Last updated: %d days ago\n", ui.RenderStatus(string(issue.Status)), daysStale)
		if issue.Assignee != "" {
			fmt.Printf("   Assignee: %s\n", issue.Assignee)
		}
		fmt.Println()
	}
}
func init() {
	staleCmd.Flags().IntP("days", "d", 30, "Issues not updated in this many days")
	staleCmd.Flags().StringP("status", "s", "", "Filter by status (open|in_progress|blocked|deferred)")
	staleCmd.Flags().IntP("limit", "n", 50, "Maximum issues to show")
	// Note: --json flag is defined as a persistent flag in main.go, not here
	rootCmd.AddCommand(staleCmd)
}
