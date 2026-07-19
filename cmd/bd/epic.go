package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"os"
)

var epicCmd = &cobra.Command{
	Use:     "epic",
	GroupID: "deps",
	Short:   "Epic management commands",
}
var epicStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show epic completion status",
	// NoArgs: `epic status` lists ALL epics' closure-eligibility; it has no
	// per-epic mode and ignores any positional arg. Without this, a stray arg
	// (`epic status <id>` / typo / garbage) was silently accepted and produced
	// the whole-workspace listing anyway (beads-qvys) — a false "filtered by id"
	// read. Matches the sibling epicCloseEligibleCmd (cobra.NoArgs).
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("epic-status")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		eligibleOnly, _ := cmd.Flags().GetBool("eligible-only")
		ctx := rootCtx

		// Proxied-server mode: 'epic' is not a noDbCommand, so the global
		// `store` is nil on hub-connected crew — reading it would nil-panic
		// (beads-92ld). Route through the UOW stack instead, mirroring the
		// direct path below (same --eligible-only + rendering).
		if usesProxiedServer() {
			return runEpicStatusProxiedServer(ctx, eligibleOnly)
		}

		epics, err := store.GetEpicsEligibleForClosure(ctx)
		if err != nil {
			return HandleErrorRespectJSON("getting epic status: %v", err)
		}
		return renderEpicStatus(epics, eligibleOnly)
	},
}

// renderEpicStatus applies --eligible-only filtering and emits the epic-status
// output (honoring --json), shared by the direct and proxied-server paths
// (beads-92ld) so they stay behaviorally identical.
func renderEpicStatus(epics []*types.EpicStatus, eligibleOnly bool) error {
	if eligibleOnly {
		filtered := []*types.EpicStatus{}
		for _, epic := range epics {
			if epic.EligibleForClose {
				filtered = append(filtered, epic)
			}
		}
		epics = filtered
	}
	if jsonOutput {
		if epics == nil {
			epics = []*types.EpicStatus{}
		}
		return outputJSON(epics)
	}
	if len(epics) == 0 {
		// beads-cudm: distinguish "no open epics at all" from "none eligible"
		// so the message is factually accurate under --eligible-only.
		if eligibleOnly {
			fmt.Println("No epics eligible for closure")
		} else {
			fmt.Println("No open epics found")
		}
		return nil
	}
	for _, epicStatus := range epics {
		epic := epicStatus.Epic
		percentage := 0
		if epicStatus.TotalChildren > 0 {
			percentage = (epicStatus.ClosedChildren * 100) / epicStatus.TotalChildren
		}
		statusIcon := ""
		if epicStatus.EligibleForClose {
			statusIcon = ui.RenderPass("✓")
		} else if percentage > 0 {
			statusIcon = ui.RenderWarn("○")
		} else {
			statusIcon = "○"
		}
		fmt.Printf("%s %s %s\n", statusIcon, ui.RenderAccent(epic.ID), ui.RenderBold(epic.Title))
		fmt.Printf("   Progress: %d/%d children closed (%d%%)\n",
			epicStatus.ClosedChildren, epicStatus.TotalChildren, percentage)
		if epicStatus.EligibleForClose {
			fmt.Printf("   %s\n", ui.RenderPass("Eligible for closure"))
		}
		fmt.Println()
	}
	return nil
}

var closeEligibleEpicsCmd = &cobra.Command{
	Use:           "close-eligible",
	Args:          cobra.NoArgs,
	Short:         "Close epics where all children are complete",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("epic-close-eligible")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if !dryRun {
			CheckReadonly("epic close-eligible")
		}
		ctx := rootCtx

		// Proxied-server mode: same nil-store hazard as `epic status`
		// (beads-92ld). Route both the read (GetEpicsEligibleForClosure) and
		// the write (CloseIssue) through the UOW, which already exposes both.
		if usesProxiedServer() {
			return runEpicCloseEligibleProxiedServer(ctx, dryRun)
		}

		epics, err := store.GetEpicsEligibleForClosure(ctx)
		if err != nil {
			return HandleErrorRespectJSON("getting eligible epics: %v", err)
		}
		// Direct path: store.CloseIssue autocommits per call (withConn commit
		// mode), so there is no batch commit step (commitFn == nil).
		return renderEpicCloseEligible(epics, dryRun, func(id string) error {
			return store.CloseIssue(ctx, id, "All children completed", "system", "")
		}, nil)
	},
}

// renderEpicCloseEligible filters to eligible epics, then previews (--dry-run)
// or closes them via closeFn, emitting the same output (honoring --json) for
// both the direct and proxied-server paths (beads-92ld). closeFn abstracts the
// per-epic close (direct store.CloseIssue vs the proxied UOW IssueUseCase).
// commitFn, if non-nil, is invoked once after at least one successful close to
// persist the batch — the proxied UOW holds a single transaction that must be
// committed explicitly (the direct store autocommits, so it passes nil).
// epicCloseEligibleResult builds the STABLE --json shape for
// `bd epic close-eligible` (beads-qos4p). All non-error outcomes emit this one
// schema — eligible (rich EpicStatus objects), closed (ids), count, dry_run —
// so a --json consumer never has to branch array-vs-dict by outcome. Empty and
// dry-run legs populate 'eligible' with closed=[]; the actually-closed leg
// populates 'closed'+'count' (and keeps 'eligible' with what it closed).
func epicCloseEligibleResult(eligible []*types.EpicStatus, closedIDs []string, dryRun bool) map[string]interface{} {
	if eligible == nil {
		eligible = []*types.EpicStatus{}
	}
	if closedIDs == nil {
		closedIDs = []string{}
	}
	return map[string]interface{}{
		"eligible": eligible,
		"closed":   closedIDs,
		"count":    len(closedIDs),
		"dry_run":  dryRun,
	}
}

func renderEpicCloseEligible(epics []*types.EpicStatus, dryRun bool, closeFn func(id string) error, commitFn func() error) error {
	var eligibleEpics []*types.EpicStatus
	for _, epic := range epics {
		if epic.EligibleForClose {
			eligibleEpics = append(eligibleEpics, epic)
		}
	}
	if len(eligibleEpics) == 0 {
		if jsonOutput {
			// beads-qos4p: emit the STABLE dict shape (same schema as dry-run
			// and actually-closed) so a --json consumer sees one static shape
			// regardless of outcome, not a bare array here + a dict elsewhere.
			return outputJSON(epicCloseEligibleResult(eligibleEpics, []string{}, true))
		}
		fmt.Println("No epics eligible for closure")
		return nil
	}
	if dryRun {
		if jsonOutput {
			// beads-qos4p: stable dict (eligible populated, closed empty, dry_run).
			return outputJSON(epicCloseEligibleResult(eligibleEpics, []string{}, true))
		}
		fmt.Printf("Would close %d epic(s):\n", len(eligibleEpics))
		for _, epicStatus := range eligibleEpics {
			fmt.Printf("  - %s: %s\n", epicStatus.Epic.ID, epicStatus.Epic.Title)
		}
		return nil
	}
	closedIDs := []string{}
	for _, epicStatus := range eligibleEpics {
		if err := closeFn(epicStatus.Epic.ID); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing %s: %v\n", epicStatus.Epic.ID, err)
			continue
		}
		closedIDs = append(closedIDs, epicStatus.Epic.ID)
	}
	if len(closedIDs) > 0 {
		commandDidWrite.Store(true)
		// Persist the proxied UOW's batch before reporting success — otherwise
		// the closes roll back when the UOW is discarded and `close-eligible`
		// would falsely report closing epics that stay open (beads-92ld).
		if commitFn != nil {
			if err := commitFn(); err != nil {
				return HandleErrorRespectJSON("committing epic closures: %v", err)
			}
		}
	}
	// All-failed guard: eligibleEpics was non-empty (the len==0 early-out
	// above already handled "nothing eligible"), so if closedIDs is empty
	// every close attempt failed. Per-item errors are already on stderr; a
	// bare rc=0 with "✓ Closed 0 epic(s)" / {"closed":[]} would be a false
	// success indistinguishable from "nothing eligible". Trip a non-zero exit,
	// mirroring the close.go all-failed path (beads-b0df).
	if len(closedIDs) == 0 {
		if jsonOutput {
			return HandleErrorRespectJSON("no epics closed (all %d eligible epic(s) failed to close)", len(eligibleEpics))
		}
		return SilentExit()
	}
	if jsonOutput {
		// beads-qos4p: same STABLE dict shape as the empty/dry-run legs — one
		// static schema regardless of outcome. 'eligible' still carries what we
		// closed so a consumer gets the rich objects too.
		return outputJSON(epicCloseEligibleResult(eligibleEpics, closedIDs, false))
	}
	fmt.Printf("✓ Closed %d epic(s)\n", len(closedIDs))
	for _, id := range closedIDs {
		fmt.Printf("  - %s\n", id)
	}
	return nil
}

func init() {
	epicCmd.AddCommand(epicStatusCmd)
	epicCmd.AddCommand(closeEligibleEpicsCmd)
	epicStatusCmd.Flags().Bool("eligible-only", false, "Show only epics eligible for closure")
	closeEligibleEpicsCmd.Flags().Bool("dry-run", false, "Preview what would be closed without making changes")
	rootCmd.AddCommand(epicCmd)
}
