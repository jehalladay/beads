package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/validation"
)

var quickCmd = &cobra.Command{
	Use:     "q [title]",
	GroupID: "issues",
	Short:   "Quick capture: create issue and output only ID",
	Long: `Quick capture creates an issue and outputs only the issue ID.
Designed for scripting and AI agent integration.

Example:
  bd q "Fix login bug"           # Outputs: bd-a1b2
  ISSUE=$(bd q "New feature")    # Capture ID in variable
  bd q "Task" | xargs bd show    # Pipe to other commands`,
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Create the event before the readonly guard so the operation label
		// matches this command ("q", not "create") and the readonly exit path
		// still flushes queued metrics via CheckReadonly's CloseAndFlush.
		evt := metrics.NewCommandEvent("q")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		CheckReadonly("q")

		title := strings.Join(args, " ")

		// beads-cra1: trim + reject a whitespace-only title, mirroring create.go
		// (beads-n5xz) and update.go. types.Validate only rejects len==0, so a
		// title like "   " (len>0) previously created a blank-displayed bead with
		// rc=0 here while `bd create "   "` / `bd update --title "   "` rejected it
		// — a q/quick asymmetry n5xz missed (it scoped the trim to
		// create.go/create_input.go/update.go, not quick.go). This runs BEFORE the
		// usesProxiedServer() split below, so ONE site covers both direct and
		// proxied modes; HandleErrorRespectJSON keeps the --json error contract.
		title = strings.TrimSpace(title)
		if title == "" {
			return HandleErrorRespectJSON("title cannot be empty")
		}

		priorityStr, _ := cmd.Flags().GetString("priority")
		issueType, _ := cmd.Flags().GetString("type")
		labels, _ := cmd.Flags().GetStringSlice("labels")

		// beads-m22rq: reject reserved gt identity labels (gt:agent/gt:role/gt:rig)
		// on a non-gt-internal write, mirroring single `bd create` (create.go:200,
		// reservedIdentityLabelError), create-form (beads-1077e), graph create
		// (beads-s13vq), and markdown import (beads-kvq0v). `bd q`/`bd quick` takes
		// a direct human `--labels` flag and mints an issue, but was the one
		// authoring seam the beads-3c4g write-time reservation never reached — so
		// `bd q "x" -l gt:agent` silently created a bead hidden from `bd ready`
		// (the exact spoof/foot-gun 3c4g closes). The guard is CLI-layer only (not a
		// shared CreateIssue chokepoint), so each create verb must apply it. This
		// runs BEFORE the usesProxiedServer() split, so ONE site covers both direct
		// and proxied modes (same as the cra1 trim + n8xi priority guards above);
		// HandleErrorRespectJSON keeps the --json error contract.
		for _, label := range labels {
			if msg := reservedIdentityLabelError(label); msg != "" {
				return HandleErrorRespectJSON("%s", msg)
			}
		}

		priority, err := validation.ValidatePriority(priorityStr)
		if err != nil {
			// beads-n8xi: bd q/quick honors --json on success (outputJSON(issue)
			// at the direct path below + the proxied path, direct-path parity
			// landed by beads-j54e) and this priority-validation guard runs BEFORE
			// the usesProxiedServer() split, so ONE fix covers both modes. Route
			// through HandleErrorRespectJSON so `bd q ... --priority=garbage --json`
			// emits a stdout JSON error object, not empty stdout + stderr text
			// (0wp9/21xi/v02z --json-error-contract class).
			return HandleErrorRespectJSON("%v", err)
		}

		issue := &types.Issue{
			Title:     title,
			Status:    types.StatusOpen,
			Priority:  priority,
			IssueType: types.IssueType(issueType).Normalize(),
			Labels:    mergeCreateLabels(labels, nil),
		}

		// beads-eh0z: in proxiedServerMode the global 'store' is nil (main.go
		// wires uowProvider and returns before store init), so store.CreateIssue
		// below fails with "storage is nil" for every hub-connected crew. Route
		// to a UOW-based handler mirroring `bd create` (create_proxied_server.go),
		// the fszd/aocj proxied-routing umbrella.
		if usesProxiedServer() {
			runQuickProxiedServer(rootCtx, issue, labels)
			return nil
		}

		ctx := rootCtx
		if err := store.CreateIssue(ctx, issue, actor); err != nil {
			// beads-wf68: route the direct-path store.CreateIssue error through
			// HandleErrorRespectJSON so `bd q/quick <t> --json` emits a stdout JSON
			// error object, not empty stdout + plain-text stderr. quick honors --json
			// on its success path (outputJSON(issue) below, beads-j54e) and its
			// priority-validation guard above (beads-n8xi), so this error path was the
			// un-updated twin; the canonical sibling create.go:584 already routes the
			// IDENTICAL store.CreateIssue error through RespectJSON. Same
			// 0wp9/21xi/v02z/n8xi --json-error-contract class (defensive parity).
			return HandleErrorRespectJSON("%v", err)
		}

		commandDidWrite.Store(true)

		// Under --json emit the full issue object like create/todo and the
		// proxied path (create_proxied_server.go), not a bare id — q/quick
		// inherit the global --json flag (and advertise it in --help) but the
		// direct path previously printed only issue.ID, byte-identical to plain
		// output, breaking scripted json.load consumers (beads-j54e). The
		// proxied quick path already emits outputJSON(result.Issue); this brings
		// the direct path to parity.
		if jsonOutput {
			return outputJSON(issue)
		}
		fmt.Println(issue.ID)
		return nil
	},
}

func init() {
	quickCmd.Flags().StringP("priority", "p", "2", "Priority (0-4 or P0-P4)")
	quickCmd.Flags().StringP("type", "t", "task", "Issue type")
	quickCmd.Flags().StringSliceP("labels", "l", []string{}, "Labels")
	rootCmd.AddCommand(quickCmd)
}
