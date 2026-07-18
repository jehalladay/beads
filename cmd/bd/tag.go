package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/ui"
)

var tagCmd = &cobra.Command{
	Use:     "tag <id> <label>",
	GroupID: "issues",
	Short:   "Add a label to an issue",
	Long: `Add a label to an issue.

Shorthand for 'bd update <id> --add-label <label>'.

Examples:
  bd tag bd-123 bug
  bd tag bd-123 needs-review`,
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("tag")

		evt := metrics.NewCommandEvent("tag")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		id := args[0]
		label := args[1]

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

		// beads-huu7: detect a no-op (label already present) BEFORE the
		// idempotent AddLabel so we can report honestly instead of a false
		// "✓ Added". A GetLabels failure degrades to the prior behavior (treat
		// as a real add). rc stays 0 — adding a present label is not an error.
		alreadyPresent := false
		if cur, gerr := issueStore.GetLabels(ctx, result.ResolvedID); gerr == nil {
			for _, l := range cur {
				if l == label {
					alreadyPresent = true
					break
				}
			}
		}

		if err := issueStore.AddLabel(ctx, result.ResolvedID, label, actor); err != nil {
			return HandleErrorRespectJSON("adding label to %s: %v", id, err)
		}
		if err := commitPendingIfEmbedded(ctx, issueStore, actor, doltAutoCommitParams{
			Command:  "tag",
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
				return outputJSON(updatedIssue)
			}
			return nil
		}
		if alreadyPresent {
			// Honest no-op: the label was already on the issue, so AddLabel
			// changed nothing. Do not print the "✓ Added" success glyph.
			fmt.Printf("• label %q already present on %s — no change\n", label, formatFeedbackID(result.ResolvedID, title))
			return nil
		}
		fmt.Printf("%s Added label %q to %s\n", ui.RenderPass("✓"), label, formatFeedbackID(result.ResolvedID, title))
		return nil
	},
}

func init() {
	tagCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(tagCmd)
}
