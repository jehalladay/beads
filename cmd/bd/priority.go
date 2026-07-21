package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/validation"
)

var priorityCmd = &cobra.Command{
	Use:     "priority <id> <n>",
	GroupID: "issues",
	Short:   "Set the priority of an issue",
	Long: `Set the priority of an issue.

Shorthand for 'bd update <id> --priority <n>'.

Priority levels:
  0 - Critical (security, data loss, broken builds)
  1 - High (major features, important bugs)
  2 - Medium (default)
  3 - Low (polish, optimization)
  4 - Backlog (future ideas)

Examples:
  bd priority bd-123 0    # Critical
  bd priority bd-123 2    # Medium`,
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("priority")

		evt := metrics.NewCommandEvent("priority")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		id := args[0]
		priorityStr := args[1]

		priority, err := validation.ValidatePriority(priorityStr)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		// beads-rejl: route to the proxied handler in proxied-server mode.
		// Without this, priority uses the direct global `store` — nil under
		// proxiedServerMode — so `bd priority` failed "storage is nil", unlike
		// its long form `bd update --priority` which routes via usesProxiedServer().
		if usesProxiedServer() {
			return runPriorityProxiedServer(rootCtx, id, priority)
		}

		ctx := rootCtx

		result, err := resolveAndGetIssueForMutation(ctx, store, id)
		if err != nil {
			if result != nil {
				result.Close()
			}
			return HandleErrorRespectJSON("resolving %s: %v", id, err)
		}
		if result == nil || result.Issue == nil {
			if result != nil {
				result.Close()
			}
			return HandleErrorRespectJSON("issue %s not found", id)
		}
		defer result.Close()

		issueStore := result.Store

		if err := validateIssueUpdatable(id, result.Issue); err != nil {
			return HandleErrorRespectJSON("%s", err)
		}

		// beads-helt4: `bd priority <id> <same-value>` is an idempotent no-op —
		// setting the priority to the value it already has changes nothing, yet the
		// command printed "✓ Set priority of ... to PN" with rc=0, a false success a
		// CI/agent gate reads as proof of a state change (the xqsy assign / bwla
		// dep-add / w2tk false-success class; `priority` was the one single-field
		// mutation verb still missing this guard). UpdateIssue itself is idempotent
		// (correct for programmatic callers), so — mirroring the sibling verbs — the
		// CLI pre-checks and reports an honest "no change" (rc=0, benign no-op) rather
		// than a fake ✓, and skips the write so no spurious audit event / commit /
		// updated_at bump is recorded. This is the stronger skip-the-write shape of
		// the single-field verbs, not the display-only bdy2 fix on `bd update`.
		// Under --json the issue object is still emitted (it accurately reflects the
		// already-desired state), preserving the array-shape JSON contract.
		if result.Issue.Priority == priority {
			SetLastTouchedID(result.ResolvedID)
			if jsonOutput {
				// beads-utby: emit an ARRAY to match the real-change path + all
				// sibling mutation verbs, not a bare DICT.
				return outputJSON([]*types.Issue{result.Issue})
			}
			fmt.Printf("%s %s already P%d (no change)\n",
				ui.RenderInfoIcon(), formatFeedbackID(result.ResolvedID, result.Issue.Title), priority)
			return nil
		}

		updates := map[string]interface{}{
			"priority": priority,
		}
		if err := issueStore.UpdateIssue(ctx, result.ResolvedID, updates, actor); err != nil {
			return HandleErrorRespectJSON("updating %s: %v", id, err)
		}
		// GC-survivable audit trail via the shared chokepoint: `bd priority`
		// changes an audited field just like `bd update -p` (beads-n4sn class).
		auditIssueUpdate(result.ResolvedID, result.Issue, updates, actor, "")
		if err := commitPendingIfEmbedded(ctx, issueStore, actor, doltAutoCommitParams{
			Command:  "priority",
			IssueIDs: []string{result.ResolvedID},
		}); err != nil {
			return HandleErrorRespectJSON("failed to commit: %v", err)
		}

		SetLastTouchedID(result.ResolvedID)

		updatedIssue, _ := issueStore.GetIssue(ctx, result.ResolvedID)
		title := ""
		if updatedIssue != nil {
			title = updatedIssue.Title
		}
		if jsonOutput {
			if updatedIssue != nil {
				// beads-utby: emit an ARRAY to match `bd update --priority`
				// (the documented long form) and the sibling mutation verbs,
				// not a bare DICT — see beads-yrtx (assign/tag).
				return outputJSON([]*types.Issue{updatedIssue})
			}
			return nil
		}
		fmt.Printf("%s Set priority of %s to P%d\n", ui.RenderPass("✓"), formatFeedbackID(result.ResolvedID, title), priority)
		return nil
	},
}

func init() {
	priorityCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(priorityCmd)
}
