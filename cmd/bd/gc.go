package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

var (
	gcDryRun    bool
	gcForce     bool
	gcOlderThan int
	gcSkipDecay bool
	gcSkipDolt  bool
)

var gcCmd = &cobra.Command{
	Use:     "gc",
	GroupID: "maint",
	Args:    maintNoArgs,
	Short:   "Garbage collect: decay old issues, compact Dolt commits, run Dolt GC",
	Long: `Full lifecycle garbage collection for standalone Beads databases.

Runs three phases in sequence:
  1. DECAY   — Delete closed issues older than N days (default 90)
  2. COMPACT — Squash old Dolt commits into fewer commits (bd compact)
  3. GC      — Run Dolt garbage collection to reclaim disk space

Each phase can be skipped individually. Use --dry-run to preview all phases
without making changes.

Examples:
  bd gc                              # Full GC with defaults (90 day decay)
  bd gc --dry-run                    # Preview what would happen
  bd gc --older-than 30              # Decay issues closed 30+ days ago
  bd gc --skip-decay                 # Skip issue deletion, just compact+GC
  bd gc --skip-dolt                  # Skip Dolt GC, just decay+compact
  bd gc --force                      # Skip confirmation prompt`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		evt := metrics.NewCommandEvent("gc")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if !gcDryRun {
			CheckReadonly("gc")
		}

		// beads-0lunb: gc is not in noDbCommands, but in proxied-server mode
		// main.go's PersistentPreRun returns before newDoltStore, leaving the
		// global `store` nil — so every store.SearchIssues/DeleteIssue/Log and the
		// UnwrapStore(store).(GarbageCollector).DoltGC call below would nil-panic
		// (the aocj proxied-routing class, same shape as branch/merge-slot).
		// Disposition is fail-loud for the WHOLE command (eng_4's routing call,
		// endorsed): gc is documented as lifecycle GC "for standalone Beads
		// databases", its headline value (DoltGC space-reclaim) is an inherently
		// local Dolt maintenance op with no proxied/UOW equivalent (like flatten's
		// GC leg), and its decay phase is a bulk destructive DeleteIssue — routing
		// that at a hub-connected crew would be a footgun, not a feature. So mirror
		// bd branch / merge-slot: refuse cleanly in proxied mode, and defensively
		// lazy-init the store on the direct path.
		if usesProxiedServer() {
			return HandleErrorRespectJSON("gc requires direct/embedded Dolt access and is not available in proxied-server mode")
		}
		if err := ensureStoreActive(); err != nil {
			return HandleErrorWithHintRespectJSON(err.Error(), diagHint())
		}

		ctx := rootCtx
		start := time.Now()

		if gcOlderThan < 0 {
			return HandleErrorRespectJSON("--older-than must be non-negative")
		}

		type phaseResult struct {
			name    string
			skipped bool
			detail  string
		}
		var results []phaseResult

		if gcSkipDecay {
			results = append(results, phaseResult{name: "Decay", skipped: true})
		} else {
			if !jsonOutput {
				fmt.Println("Phase 1/3: Decay (delete old closed issues)")
			}

			cutoffDays := gcOlderThan
			cutoffTime := time.Now().UTC().AddDate(0, 0, -cutoffDays)
			statusClosed := types.StatusClosed
			filter := types.IssueFilter{
				Status:       &statusClosed,
				ClosedBefore: &cutoffTime,
			}

			closedIssues, err := store.SearchIssues(ctx, "", filter)
			if err != nil {
				return HandleErrorRespectJSON("searching closed issues: %v", err)
			}

			var stats closedDeletionCandidateStats
			closedIssues, stats = filterClosedDeletionCandidates(closedIssues, &cutoffTime)
			warnClosedDeletionSafetySkips(stats)

			if len(closedIssues) == 0 {
				detail := fmt.Sprintf("  No closed issues older than %d days", cutoffDays)
				if !jsonOutput {
					fmt.Println(detail)
				}
				results = append(results, phaseResult{name: "Decay", detail: "0 issues deleted"})
			} else {
				if gcDryRun {
					detail := fmt.Sprintf("  Would delete %d closed issue(s)", len(closedIssues))
					if !jsonOutput {
						fmt.Println(detail)
					}
					results = append(results, phaseResult{name: "Decay", detail: fmt.Sprintf("%d issues (dry-run)", len(closedIssues))})
				} else {
					if !gcForce {
						return HandleErrorWithHintRespectJSON(
							fmt.Sprintf("would delete %d closed issue(s) older than %d days", len(closedIssues), cutoffDays),
							"Use --force to confirm or --dry-run to preview.")
					}

					deleted := 0
					for _, issue := range closedIssues {
						if err := store.DeleteIssue(ctx, issue.ID); err != nil {
							WarnError("failed to delete %s: %v", issue.ID, err)
						} else {
							deleted++
						}
					}
					commandDidWrite.Store(true)
					detail := fmt.Sprintf("  Deleted %d issue(s)", deleted)
					if !jsonOutput {
						fmt.Println(detail)
					}
					results = append(results, phaseResult{name: "Decay", detail: fmt.Sprintf("%d issues deleted", deleted)})

					if deleted > 0 {
						commandDidWrite.Store(true)
					}
				}
			}
			if !jsonOutput {
				fmt.Println()
			}
		}

		if !jsonOutput {
			fmt.Println("Phase 2/3: Compact (Dolt commit history info)")
		}

		commitCount := 0
		logEntries, logErr := store.Log(ctx, 0)
		if logErr != nil {
			WarnError("could not read Dolt commit log: %v", logErr)
		} else {
			commitCount = len(logEntries)
		}

		if commitCount <= 1 {
			if !jsonOutput {
				fmt.Printf("  Only %d commit(s), nothing to compact\n\n", commitCount)
			}
			results = append(results, phaseResult{name: "Compact", detail: "nothing to compact"})
		} else {
			if gcDryRun {
				if !jsonOutput {
					fmt.Printf("  %d commits in history (use bd flatten to squash)\n\n", commitCount)
				}
				results = append(results, phaseResult{name: "Compact", detail: fmt.Sprintf("%d commits (dry-run)", commitCount)})
			} else {
				if !jsonOutput {
					fmt.Printf("  %d commits in history\n", commitCount)
					fmt.Printf("  Tip: use 'bd flatten' to squash all history to one commit\n\n")
				}
				results = append(results, phaseResult{name: "Compact", detail: fmt.Sprintf("%d commits", commitCount)})
			}
		}

		if gcSkipDolt {
			results = append(results, phaseResult{name: "Dolt GC", skipped: true})
		} else {
			if !jsonOutput {
				fmt.Println("Phase 3/3: Dolt GC (reclaim disk space)")
			}

			gc, ok := storage.UnwrapStore(store).(storage.GarbageCollector)
			if !ok {
				if !jsonOutput {
					fmt.Println("  Storage backend does not support GC, skipping")
				}
				results = append(results, phaseResult{name: "Dolt GC", detail: "not supported"})
			} else if gcDryRun {
				if !jsonOutput {
					fmt.Println("  Would run DOLT_GC()")
				}
				results = append(results, phaseResult{name: "Dolt GC", detail: "dry-run"})
			} else {
				if err := gc.DoltGC(ctx); err != nil {
					WarnError("dolt gc failed: %v", err)
					results = append(results, phaseResult{name: "Dolt GC", detail: "failed"})
				} else {
					if !jsonOutput {
						fmt.Println("  Done")
					}
					results = append(results, phaseResult{name: "Dolt GC", detail: "complete"})
				}
			}
			if !jsonOutput {
				fmt.Println()
			}
		}

		elapsed := time.Since(start)

		if jsonOutput {
			summaryMap := make(map[string]interface{})
			summaryMap["dry_run"] = gcDryRun
			summaryMap["elapsed_ms"] = elapsed.Milliseconds()
			phases := make([]map[string]interface{}, 0, len(results))
			for _, r := range results {
				p := map[string]interface{}{
					"name":    r.name,
					"skipped": r.skipped,
				}
				if r.detail != "" {
					p["detail"] = r.detail
				}
				phases = append(phases, p)
			}
			summaryMap["phases"] = phases
			return outputJSON(summaryMap)
		}

		mode := "✓ GC complete"
		if gcDryRun {
			mode = "DRY RUN complete"
		}
		fmt.Printf("%s (%v)\n", mode, elapsed.Round(time.Millisecond))
		for _, r := range results {
			if r.skipped {
				fmt.Printf("  %s: skipped\n", r.name)
			} else {
				fmt.Printf("  %s: %s\n", r.name, r.detail)
			}
		}
		return nil
	},
}

func init() {
	gcCmd.Flags().BoolVar(&gcDryRun, "dry-run", false, "Preview without making changes")
	gcCmd.Flags().BoolVarP(&gcForce, "force", "f", false, "Skip confirmation prompts")
	gcCmd.Flags().IntVar(&gcOlderThan, "older-than", 90, "Delete closed issues older than N days")
	gcCmd.Flags().BoolVar(&gcSkipDecay, "skip-decay", false, "Skip issue deletion phase")
	gcCmd.Flags().BoolVar(&gcSkipDolt, "skip-dolt", false, "Skip Dolt garbage collection phase")

	rootCmd.AddCommand(gcCmd)
}
