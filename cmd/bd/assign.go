package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/ui"
)

// normalizeAssignee maps the unassigned sentinel "none" (case-insensitive) to
// the empty string, so `bd assign <id> none` unassigns — matching the query/list
// semantics where assignee=none means unassigned (beads-19g). "" already means
// unassign.
//
// It also TRIMS surrounding whitespace from real values (beads-llzt): the
// read/filter side matches case-insensitively but never trims
// (sqlbuild/filter.go LOWER(assignee)=LOWER(?), evaluator.go EqualFold), so a
// padded `-a "  alice  "` stored verbatim is PERMANENTLY UNMATCHABLE by
// `bd ready/list --assignee alice` — silently orphaning work from the assignee
// who is meant to pull it. This is the assignee sibling of the label-trim class
// (beads-13zc/4g2h); assignee has no single storage chokepoint (writes flow
// through a generic updates["assignee"] map + a create struct field), so the
// three CLI input sites (assign/create/update) are the seam. Trimming before
// the "none" check also folds a padded `" none "` to unassign.
func normalizeAssignee(name string) string {
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "none") {
		return ""
	}
	return name
}

var assignCmd = &cobra.Command{
	Use:     "assign <id> <name>",
	GroupID: "issues",
	Short:   "Assign an issue to someone",
	Long: `Assign an issue to someone.

Shorthand for 'bd update <id> --assignee <name>'.

Examples:
  bd assign bd-123 alice
  bd assign bd-123 ""      # unassign`,
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("assign")

		id := args[0]
		// beads-19g: "none" is the unassigned sentinel in query/list
		// (assignee=none means unassigned), so treat `bd assign <id> none` as an
		// unassign (like the documented ""), rather than storing a literal user
		// "none" that is invisible to both assignee=none and real-assignee searches.
		assignee := normalizeAssignee(args[1])

		evt := metrics.NewCommandEvent("assign")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

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

		updates := map[string]interface{}{
			"assignee": assignee,
		}
		if err := issueStore.UpdateIssue(ctx, result.ResolvedID, updates, actor); err != nil {
			return HandleErrorRespectJSON("updating %s: %v", id, err)
		}

		if err := commitPendingIfEmbedded(ctx, issueStore, actor, doltAutoCommitParams{
			Command:  "assign",
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
				if err := outputJSON(updatedIssue); err != nil {
					return err
				}
			}
		} else {
			if assignee == "" {
				fmt.Printf("%s Unassigned %s\n", ui.RenderPass("✓"), formatFeedbackID(result.ResolvedID, title))
			} else {
				fmt.Printf("%s Assigned %s to %s\n", ui.RenderPass("✓"), formatFeedbackID(result.ResolvedID, title), assignee)
			}
		}
		return nil
	},
}

func init() {
	assignCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(assignCmd)
}
