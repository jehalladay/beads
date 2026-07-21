package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var promoteCmd = &cobra.Command{
	Use:     "promote <wisp-id>",
	GroupID: "issues",
	Short:   "Promote a wisp to a permanent bead",
	Long: `Promote a wisp (ephemeral issue) to a permanent bead.

This copies the issue from the wisps table (dolt_ignored) to the permanent
issues table (Dolt-versioned), preserving labels, dependencies, events, and
comments. The original ID is preserved so all links keep working.

A comment is added recording the promotion and optional reason.

Examples:
  bd promote bd-wisp-abc123
  bd promote bd-wisp-abc123 --reason "Worth tracking long-term"`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("promote")

		evt := metrics.NewCommandEvent("promote")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		id := args[0]
		reason, _ := cmd.Flags().GetString("reason")
		// beads-tg1js: --reason is optional with no default, so a whitespace-only
		// value collapses to no-reason (mirrors reopen/5rix3 + in93a) — else the
		// "if reason != \"\"" guard below appends a dangling ":    " suffix to the
		// promotion comment. A genuine reason is kept VERBATIM (beln6). Normalized
		// here so both the direct and proxied (runPromoteProxiedServer) paths agree.
		reason = normalizeOptionalReason(reason)

		ctx := rootCtx

		// beads-aocj: on hub-connected crew the global `store` is nil in
		// proxiedServerMode, so route through the proxied UOW handler (mirrors
		// close/comment/label). Without this the direct path below fails
		// "database not initialized".
		if usesProxiedServer() {
			return runPromoteProxiedServer(ctx, id, reason)
		}

		if store == nil {
			return HandleErrorWithHint("database not initialized", diagHint())
		}

		fullID, err := utils.ResolvePartialID(ctx, store, id)
		if err != nil {
			return HandleErrorRespectJSON("resolving %s: %v", id, err)
		}

		issue, err := store.GetIssue(ctx, fullID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return HandleErrorRespectJSON("issue %s not found", fullID)
			}
			return HandleErrorRespectJSON("getting issue %s: %v", fullID, err)
		}
		if !issue.Ephemeral {
			return HandleErrorRespectJSON("%s is not a wisp (already persistent)", fullID)
		}

		comment := "Promoted from wisp to permanent bead"
		if reason != "" {
			comment += ": " + reason
		}
		// beads-9l1it: record the promotion (and user --reason) in the COMMENTS
		// table, NOT the events table — bd show / bd comments read
		// GetIssueComments (comments table); an EventCommented row no read surface
		// surfaces would leave the documented "A comment is added recording the
		// promotion and optional reason." silently invisible.
		//
		// beads-kdvfe: promote AND its recording comment run in ONE transaction
		// (PromoteFromEphemeralWithComment) so a comment-write failure rolls back
		// the promotion instead of leaving the bead promoted with the audit comment
		// silently dropped under a success (RC=0) exit. This was two independent
		// commits (PromoteFromEphemeral then AddIssueComment, the latter downgraded
		// to a stderr Warning); the proxied path (runPromoteProxiedServer) was
		// already atomic via a single uw.Commit — this brings direct to parity.
		if _, err := store.PromoteFromEphemeralWithComment(ctx, fullID, actor, comment); err != nil {
			return HandleErrorRespectJSON("promoting %s: %v", fullID, err)
		}

		commandDidWrite.Store(true)

		if jsonOutput {
			updated, _ := store.GetIssue(ctx, fullID)
			if updated != nil {
				return outputJSON(updated)
			}
			return nil
		}
		fmt.Printf("%s Promoted %s to permanent bead\n", ui.RenderPass("✓"), fullID)
		return nil
	},
}

func init() {
	promoteCmd.Flags().StringP("reason", "r", "", "Reason for promotion")
	promoteCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(promoteCmd)
}
