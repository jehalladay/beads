package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
)

var (
	flattenDryRun bool
	flattenForce  bool
)

var flattenCmd = &cobra.Command{
	Use:     "flatten",
	GroupID: "maint",
	Args:    maintNoArgs,
	Short:   "Squash all Dolt history into a single commit",
	Long: `Nuclear option: squash ALL Dolt commit history into a single commit.

This uses the Tim Sehn recipe:
  1. Flush any uncommitted working set into a commit first
  2. Create a new branch from the current state
  3. Soft-reset to the initial commit (preserving all data)
  4. Commit everything as a single snapshot
  5. Swap main branch to the new flattened branch
  6. Run Dolt GC to reclaim space from old history

This is irreversible — all commit HISTORY is lost, but no current data is
lost: pending changes under batch/off Dolt auto-commit are flushed first,
so the resulting single commit contains all current data.

Use this when:
  - Your .beads/dolt directory has grown very large
  - You don't need commit-level history (time travel)
  - You want to start fresh with minimal storage

Examples:
  bd flatten --dry-run               # Preview: show commit count and disk usage
  bd flatten --force                 # Actually squash all history
  bd flatten --force --json          # JSON output`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		evt := metrics.NewCommandEvent("flatten")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if !flattenDryRun {
			CheckReadonly("flatten")
		}

		// beads-94s8d: flatten is not in noDbCommands, so in proxied-server mode
		// main.go PersistentPreRun returns before newDoltStore, leaving the global
		// `store` nil — `storage.UnwrapStore(store).(storage.Flattener)` below then
		// yields ok=false and flatten reports the MISLEADING "storage backend does
		// not support flatten" (the Dolt backend supports it fine; store is nil
		// only because of proxied mode). This is the aocj proxied-routing class
		// (maintenance-cmd leg): flatten's history-squash + DoltGC are inherently
		// LOCAL Dolt maintenance ops with no proxied/UOW equivalent, so — like
		// `bd branch` (beads-jr2h4), `bd gc` (beads-0lunb, whose comment even cites
		// flatten as a sibling), and `bd compact` — refuse cleanly with a clear,
		// purpose-built message instead of the misleading backend-capability error.
		if usesProxiedServer() {
			return HandleErrorRespectJSON("flatten requires direct/embedded Dolt access and is not available in proxied-server mode")
		}

		ctx := rootCtx
		start := time.Now()

		flattener, ok := storage.UnwrapStore(store).(storage.Flattener)
		if !ok {
			return HandleErrorRespectJSON("storage backend does not support flatten")
		}

		logEntries, logErr := store.Log(ctx, 0)
		if logErr != nil {
			return HandleErrorRespectJSON("failed to read commit log: %v", logErr)
		}
		commitCount := len(logEntries)

		var initialHash string
		if commitCount > 0 {
			initialHash = logEntries[commitCount-1].Hash
		}

		if flattenDryRun {
			if jsonOutput {
				return outputJSON(map[string]interface{}{
					"dry_run":       true,
					"commit_count":  commitCount,
					"initial_hash":  initialHash,
					"would_flatten": commitCount > 1,
				})
			}
			fmt.Printf("DRY RUN — Flatten preview\n\n")
			fmt.Printf("  Commits:        %d\n", commitCount)
			fmt.Printf("  Initial commit: %s\n", initialHash)
			if commitCount <= 1 {
				fmt.Printf("\n  Already flat (1 commit). Nothing to do.\n")
			} else {
				fmt.Printf("\n  Would squash %d commits into 1.\n", commitCount)
				fmt.Printf("  Run with --force to proceed.\n")
			}
			return nil
		}

		if commitCount <= 1 {
			if jsonOutput {
				return outputJSON(map[string]interface{}{
					"success":      true,
					"message":      "already flat",
					"commit_count": commitCount,
				})
			}
			fmt.Println("Already flat (1 commit). Nothing to do.")
			return nil
		}

		if !flattenForce {
			return HandleErrorWithHintRespectJSON(
				fmt.Sprintf("would squash %d commits into 1 (irreversible)", commitCount),
				"Use --force to confirm or --dry-run to preview.")
		}

		if !jsonOutput {
			fmt.Printf("Flattening %d commits...\n", commitCount)
		}

		if err := flattener.Flatten(ctx); err != nil {
			return HandleErrorRespectJSON("flatten failed: %v", err)
		}

		if gc, ok := storage.UnwrapStore(store).(storage.GarbageCollector); ok {
			if err := gc.DoltGC(ctx); err != nil {
				WarnError("dolt gc after flatten failed: %v", err)
			}
		}

		elapsed := time.Since(start)

		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"success":        true,
				"commits_before": commitCount,
				"commits_after":  1,
				"elapsed_ms":     elapsed.Milliseconds(),
			})
		}
		fmt.Printf("✓ Flattened %d commits → 1\n", commitCount)
		fmt.Printf("  Time: %v\n", elapsed.Round(time.Millisecond))
		return nil
	},
}

func init() {
	flattenCmd.Flags().BoolVar(&flattenDryRun, "dry-run", false, "Preview without making changes")
	flattenCmd.Flags().BoolVarP(&flattenForce, "force", "f", false, "Confirm irreversible history squash")

	rootCmd.AddCommand(flattenCmd)
}
