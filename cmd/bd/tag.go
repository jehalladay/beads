package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
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

		// beads-0tm9z: reject reserved gt identity labels (gt:agent/gt:role/gt:rig)
		// on the tag verb, matching single create (create.go:200), label add
		// (label.go:277), and the mutation-path guards. `bd tag` is documented as
		// shorthand for `bd update --add-label`, but it has its OWN RunE and does
		// NOT route through update.go's chokepoint, so the update-path guard would
		// not cover it. Guard here — after CheckReadonly, BEFORE the
		// usesProxiedServer() split — so ONE site covers the direct + proxied paths.
		// GT_INTERNAL bypass preserved (gt's own stamps still work).
		if msg := reservedIdentityLabelError(args[1]); msg != "" {
			return HandleErrorRespectJSON("%s", msg)
		}
		// beads-4sfae: reserve the 'provides:' capability family on the tag verb
		// too — `bd tag <id> provides:<cap>` is `bd update --add-label` shorthand
		// with its OWN RunE (doesn't route through update.go), so it needs the same
		// provides: reservation beads-o70m1 added at create/graph and label.go
		// enforces on `bd label add`. Same pre-proxied-split placement as the
		// identity guard above.
		if msg := providesLabelError(args[1]); msg != "" {
			return HandleErrorRespectJSON("%s", msg)
		}

		evt := metrics.NewCommandEvent("tag")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		// beads-aocj: route to the proxied handler in proxied-server mode.
		// Without this, tag uses the direct global `store` — nil under
		// proxiedServerMode — so `bd tag` failed "storage is nil", unlike its
		// long form `bd update --add-label` which routes via usesProxiedServer().
		if usesProxiedServer() {
			return runTagProxiedServer(rootCtx, args)
		}

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
		// idempotent AddLabel so the human view reports honestly instead of a
		// false "✓ Added". AddLabel is idempotent (AddLabelInTx no-ops when the
		// label is already present), so a present-label tag writes nothing — but
		// the CLI printed "✓ Added" regardless, which a CI/agent gate misreads as
		// proof the label was newly applied. Mirrors the label-add no-op fix
		// (beads-qi8t) + label-remove (beads-yaux). rc stays 0 (not an error). A
		// GetLabels pre-read failure degrades to the prior behavior (report
		// "added") so a hiccup can't block tagging.
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
				// beads-yrtx: emit an ARRAY to match `bd update --add-label`
				// and the sibling mutation verbs, not a bare DICT.
				return outputJSON([]*types.Issue{updatedIssue})
			}
			return nil
		}
		if alreadyPresent {
			// Honest no-op: the label was already on the issue, so AddLabel wrote
			// nothing. No success glyph (beads-huu7).
			fmt.Printf("%s label %q already present on %s (no change)\n", ui.RenderInfoIcon(), label, formatFeedbackID(result.ResolvedID, title))
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
