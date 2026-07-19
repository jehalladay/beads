package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/ui"
)

// mergeSlotCmd is the parent command for merge-slot operations
var mergeSlotCmd = &cobra.Command{
	Use:     "merge-slot",
	GroupID: "issues",
	Short:   "Manage merge-slot gates for serialized conflict resolution",
	Long: `Merge-slot gates serialize conflict resolution in the merge queue.

A merge slot is an exclusive access primitive: only one agent can hold it at a time.
This prevents "monkey knife fights" where multiple polecats race to resolve conflicts
and create cascading conflicts.

Each rig has one merge slot bead: <prefix>-merge-slot (labeled gt:slot).
The slot uses:
  - status=open: slot is available
  - status=in_progress: slot is held
  - metadata.holder: who currently holds the slot
  - metadata.waiters: priority-ordered queue of waiters

Examples:
  bd merge-slot create              # Create merge slot for current rig
  bd merge-slot check               # Check if slot is available
  bd merge-slot acquire             # Try to acquire the slot
  bd merge-slot release             # Release the slot`,
}

var mergeSlotCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a merge slot bead for the current rig",
	Long: `Create a merge slot bead for serialized conflict resolution.

The slot ID is automatically generated based on the beads prefix (e.g., gt-merge-slot).
The slot is created with status=open (available).`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMergeSlotCreate,
}

var mergeSlotCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check merge slot availability",
	Long: `Check if the merge slot is available or held.

Returns:
  - available: slot can be acquired
  - held by <holder>: slot is currently held
  - not found: no merge slot exists for this rig`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMergeSlotCheck,
}

var mergeSlotAcquireCmd = &cobra.Command{
	Use:   "acquire",
	Short: "Acquire the merge slot",
	Long: `Attempt to acquire the merge slot for exclusive access.

If the slot is available (status=open), it will be acquired:
  - status set to in_progress
  - holder set to the requester

If the slot is held (status=in_progress), the command fails unless
--wait is passed, which adds the requester to the waiters queue.

Use --holder to specify who is acquiring (default: BEADS_ACTOR env var).`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMergeSlotAcquire,
}

var mergeSlotReleaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Release the merge slot",
	Long: `Release the merge slot after conflict resolution is complete.

Sets status back to open and clears the holder field.
If there are waiters, the highest-priority waiter should then acquire.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMergeSlotRelease,
}

var (
	mergeSlotHolder    string
	mergeSlotAddWaiter bool
)

func init() {
	mergeSlotAcquireCmd.Flags().StringVar(&mergeSlotHolder, "holder", "", "Who is acquiring the slot (default: BEADS_ACTOR)")
	mergeSlotAcquireCmd.Flags().BoolVar(&mergeSlotAddWaiter, "wait", false, "Add to waiters list if slot is held")
	mergeSlotReleaseCmd.Flags().StringVar(&mergeSlotHolder, "holder", "", "Who is releasing the slot (for verification)")

	mergeSlotCmd.AddCommand(mergeSlotCreateCmd)
	mergeSlotCmd.AddCommand(mergeSlotCheckCmd)
	mergeSlotCmd.AddCommand(mergeSlotAcquireCmd)
	mergeSlotCmd.AddCommand(mergeSlotReleaseCmd)
	rootCmd.AddCommand(mergeSlotCmd)
}

func runMergeSlotCreate(cmd *cobra.Command, args []string) error {
	CheckReadonly("merge-slot create")

	evt := metrics.NewCommandEvent("merge-slot-create")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	issue, err := store.MergeSlotCreate(rootCtx, actor)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	commandDidWrite.Store(true)

	if jsonOutput {
		result := map[string]interface{}{
			"id":     issue.ID,
			"status": string(issue.Status),
		}
		return outputJSON(result)
	}

	fmt.Printf("%s Created merge slot: %s\n", ui.RenderPass("✓"), issue.ID)
	return nil
}

func runMergeSlotCheck(cmd *cobra.Command, args []string) error {
	evt := metrics.NewCommandEvent("merge-slot-check")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	status, err := store.MergeSlotCheck(rootCtx)
	if err != nil {
		if isNotFoundErr(err) {
			slotID := storage.MergeSlotID(rootCtx, store)
			if jsonOutput {
				// beads-8qf2q: emit the SAME key-set as the found leg below so a
				// consumer can statically type `merge-slot check --json` regardless
				// of whether the slot exists. Previously not-found emitted
				// {id,available,error} while found emitted {id,available,holder,
				// waiters} — a key-set flip (a consumer keying on .holder/.waiters
				// got missing keys on not-found, and vice-versa on .error). Keep
				// holder/waiters null on not-found; error is null on the found leg.
				result := map[string]interface{}{
					"id":        slotID,
					"available": false,
					"holder":    nil,
					"waiters":   nil,
					"error":     "not found",
				}
				return outputJSON(result)
			}
			fmt.Printf("Merge slot not found: %s\n", slotID)
			fmt.Printf("Run 'bd merge-slot create' to create one.\n")
			return nil
		}
		return HandleErrorRespectJSON("%v", err)
	}

	if jsonOutput {
		// beads-8qf2q: stable key-set — include "error":nil so the found and
		// not-found legs emit the identical key-set (see the not-found leg above).
		result := map[string]interface{}{
			"id":        status.SlotID,
			"available": status.Available,
			"holder":    nilIfEmpty(status.Holder),
			"waiters":   status.Waiters,
			"error":     nil,
		}
		return outputJSON(result)
	}

	if status.Available {
		fmt.Printf("%s Merge slot available: %s\n", ui.RenderPass("✓"), status.SlotID)
	} else {
		fmt.Printf("%s Merge slot held: %s\n", ui.RenderAccent("○"), status.SlotID)
		fmt.Printf("  Holder: %s\n", status.Holder)
		if len(status.Waiters) > 0 {
			fmt.Printf("  Waiters: %d\n", len(status.Waiters))
			for i, w := range status.Waiters {
				fmt.Printf("    %d. %s\n", i+1, w)
			}
		}
	}

	return nil
}

func runMergeSlotAcquire(cmd *cobra.Command, args []string) error {
	CheckReadonly("merge-slot acquire")

	evt := metrics.NewCommandEvent("merge-slot-acquire")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	holder := mergeSlotHolder
	if holder == "" {
		holder = actor
	}
	if holder == "" {
		return HandleErrorRespectJSON("no holder specified; use --holder or set BEADS_ACTOR env var")
	}

	result, err := store.MergeSlotAcquire(rootCtx, holder, actor, mergeSlotAddWaiter)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	if !result.Acquired && !result.Waiting {
		if jsonOutput {
			out := map[string]interface{}{
				"id":       result.SlotID,
				"acquired": false,
				"holder":   result.Holder,
			}
			if eerr := outputJSON(out); eerr != nil {
				return eerr
			}
			return SilentExit()
		}
		return HandleErrorWithHint(
			fmt.Sprintf("slot held by: %s", result.Holder),
			"Use --wait to add yourself to the waiters queue.")
	}

	if result.Waiting {
		if jsonOutput {
			out := map[string]interface{}{
				"id":       result.SlotID,
				"acquired": false,
				"waiting":  true,
				"holder":   result.Holder,
				"position": result.Position,
			}
			if eerr := outputJSON(out); eerr != nil {
				return eerr
			}
			return SilentExit()
		}
		fmt.Printf("%s Slot held by %s, added to waiters queue (position %d)\n",
			ui.RenderAccent("○"), result.Holder, result.Position)
		return SilentExit()
	}

	// Successfully acquired.
	commandDidWrite.Store(true)

	if jsonOutput {
		out := map[string]interface{}{
			"id":       result.SlotID,
			"acquired": true,
			"holder":   holder,
		}
		return outputJSON(out)
	}

	fmt.Printf("%s Acquired merge slot: %s\n", ui.RenderPass("✓"), result.SlotID)
	fmt.Printf("  Holder: %s\n", holder)
	return nil
}

func runMergeSlotRelease(cmd *cobra.Command, args []string) error {
	CheckReadonly("merge-slot release")

	evt := metrics.NewCommandEvent("merge-slot-release")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	if err := store.MergeSlotRelease(rootCtx, mergeSlotHolder, actor); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	commandDidWrite.Store(true)

	if jsonOutput {
		slotID := storage.MergeSlotID(rootCtx, store)
		out := map[string]interface{}{
			"id":       slotID,
			"released": true,
		}
		return outputJSON(out)
	}

	slotID := storage.MergeSlotID(rootCtx, store)
	fmt.Printf("%s Released merge slot: %s\n", ui.RenderPass("✓"), slotID)
	return nil
}

// nilIfEmpty returns nil if s is empty, otherwise returns s.
// Used for JSON output where empty strings should be null.
func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
