package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// CleanupEmptyResponse is returned when there are no closed issues to delete
type CleanupEmptyResponse struct {
	DeletedCount int    `json:"deleted_count"`
	Message      string `json:"message"`
	Filter       string `json:"filter,omitempty"`
	Ephemeral    bool   `json:"ephemeral,omitempty"`
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Delete closed issues to reduce database size",
	Long: `Delete closed issues to reduce database size.

This command permanently removes closed issues from the database.

NOTE: This command only manages issue lifecycle (closed -> deleted). For general
health checks and automatic repairs, use 'bd doctor --fix' instead.

By default, deletes ALL closed issues. Use --older-than to only delete
issues closed before a certain date.

EXAMPLES:
  bd admin cleanup --force                          # Delete all closed issues
  bd admin cleanup --older-than 30 --force          # Only issues closed 30+ days ago
  bd admin cleanup --ephemeral --force              # Only closed wisps (transient molecules)
  bd admin cleanup --dry-run                        # Preview what would be deleted

SAFETY:
- Requires --force flag to actually delete (unless --dry-run)
- Supports --cascade to delete dependents
- Shows preview of what will be deleted
- Use --json for programmatic output

SEE ALSO:
  bd doctor --fix    Automatic health checks and repairs (recommended for routine maintenance)
  bd admin compact   Compact old closed issues to save space`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("admin-cleanup")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if err := requireServerMode("cleanup"); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		force, _ := cmd.Flags().GetBool("force")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		cascade, _ := cmd.Flags().GetBool("cascade")
		olderThanDays, _ := cmd.Flags().GetInt("older-than")
		wispOnly, _ := cmd.Flags().GetBool("ephemeral")

		if store == nil {
			if err := ensureStoreActive(); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
		}

		ctx := rootCtx

		statusClosed := types.StatusClosed
		filter := types.IssueFilter{
			Status: &statusClosed,
		}

		if olderThanDays > 0 {
			cutoffTime := time.Now().AddDate(0, 0, -olderThanDays)
			filter.ClosedBefore = &cutoffTime
		}

		if wispOnly {
			wispTrue := true
			filter.Ephemeral = &wispTrue
		}

		closedIssues, err := store.SearchIssues(ctx, "", filter)
		if err != nil {
			return HandleErrorRespectJSON("listing issues: %v", err)
		}

		pinnedCount := 0
		filteredIssues := make([]*types.Issue, 0, len(closedIssues))
		for _, issue := range closedIssues {
			if issue.Pinned {
				pinnedCount++
				continue
			}
			filteredIssues = append(filteredIssues, issue)
		}
		closedIssues = filteredIssues

		if pinnedCount > 0 && !jsonOutput {
			fmt.Printf("Skipping %d pinned issue(s) (protected from cleanup)\n", pinnedCount)
		}

		if len(closedIssues) == 0 {
			if jsonOutput {
				result := CleanupEmptyResponse{
					DeletedCount: 0,
					Message:      "No closed issues to delete",
				}
				if olderThanDays > 0 {
					result.Filter = fmt.Sprintf("older than %d days", olderThanDays)
				}
				if wispOnly {
					result.Ephemeral = true
				}
				if err := outputJSON(result); err != nil {
					return err
				}
			} else {
				msg := "No closed issues to delete"
				if wispOnly && olderThanDays > 0 {
					msg = fmt.Sprintf("No closed wisps older than %d days to delete", olderThanDays)
				} else if wispOnly {
					msg = "No closed wisps to delete"
				} else if olderThanDays > 0 {
					msg = fmt.Sprintf("No closed issues older than %d days to delete", olderThanDays)
				}
				fmt.Println(msg)
			}
			return nil
		}

		issueIDs := make([]string, len(closedIssues))
		for i, issue := range closedIssues {
			issueIDs[i] = issue.ID
		}

		// Cascade in cleanup must stay within the closed-only contract:
		// cleanup deletes CLOSED cruft, so a cascade must never reach an OPEN
		// (or pinned) dependent. The storage-layer cascade has no status
		// filter (correct for `bd delete <id> --cascade`, wrong here), so we
		// pre-expand to the closed-only dependent closure ourselves and hand
		// deleteBatch an explicit set with cascade disabled. (beads-9r3f)
		if cascade {
			expanded, skippedOpen, err := expandClosedOnlyCascade(ctx, store, issueIDs)
			if err != nil {
				return HandleErrorRespectJSON("expanding cascade set: %v", err)
			}
			issueIDs = expanded
			if len(skippedOpen) > 0 && !jsonOutput {
				fmt.Printf("Skipping %d non-closed dependent(s) that cascade would otherwise delete "+
					"(cleanup only removes closed issues; use 'bd delete --cascade' to delete open dependents deliberately)\n",
					len(skippedOpen))
			}
			// The set is now the exact closed-only closure; delete it verbatim.
			cascade = false
		}

		if !force && !dryRun {
			issueType := "closed"
			if wispOnly {
				issueType = "closed wisp"
			}
			return HandleErrorWithHintRespectJSON(
				fmt.Sprintf("would delete %d %s issue(s)", len(issueIDs), issueType),
				"Use --force to confirm or --dry-run to preview.")
		}

		if !jsonOutput {
			issueType := "closed"
			if wispOnly {
				issueType = "closed wisp"
			}
			if olderThanDays > 0 {
				fmt.Printf("Found %d %s issue(s) older than %d days\n", len(closedIssues), issueType, olderThanDays)
			} else {
				fmt.Printf("Found %d %s issue(s)\n", len(closedIssues), issueType)
			}
			if dryRun {
				fmt.Println(ui.RenderWarn("DRY RUN - no changes will be made"))
			}
			fmt.Println()
		}

		if err := deleteBatch(cmd, issueIDs, force, dryRun, cascade, jsonOutput, false, "cleanup"); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		return nil
	},
}

// expandClosedOnlyCascade computes the recursive dependent closure of seedIDs
// (which cleanup guarantees are closed, non-pinned) but retains ONLY dependents
// that are themselves closed and not pinned. This keeps `bd admin cleanup
// --cascade` within cleanup's closed-only contract: it never deletes an open or
// pinned issue, even one that depends on a closed issue. Returns the expanded
// closed-only ID set and the list of skipped (open/pinned) dependent IDs.
//
// Contrast `bd delete <id> --cascade`, whose storage-layer cascade deliberately
// deletes ALL dependents regardless of status — that path is unchanged. (beads-9r3f)
func expandClosedOnlyCascade(ctx context.Context, s storage.DoltStorage, seedIDs []string) (expanded []string, skippedOpen []string, err error) {
	inSet := make(map[string]bool, len(seedIDs))
	for _, id := range seedIDs {
		inSet[id] = true
	}
	skipped := make(map[string]bool)

	queue := append([]string{}, seedIDs...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]

		dependents, derr := s.GetDependents(ctx, id)
		if derr != nil {
			return nil, nil, fmt.Errorf("get dependents of %s: %w", id, derr)
		}
		for _, dep := range dependents {
			if inSet[dep.ID] || skipped[dep.ID] {
				continue
			}
			// Only cascade INTO closed, non-pinned dependents. An open (or
			// pinned) dependent is live/protected work — cleanup must not
			// touch it, and we must not traverse through it either.
			if dep.Status != types.StatusClosed || dep.Pinned {
				skipped[dep.ID] = true
				continue
			}
			inSet[dep.ID] = true
			queue = append(queue, dep.ID)
		}
	}

	expanded = make([]string, 0, len(inSet))
	for id := range inSet {
		expanded = append(expanded, id)
	}
	skippedOpen = make([]string, 0, len(skipped))
	for id := range skipped {
		skippedOpen = append(skippedOpen, id)
	}
	return expanded, skippedOpen, nil
}

func init() {
	cleanupCmd.Flags().BoolP("force", "f", false, "Actually delete (without this flag, shows error)")
	cleanupCmd.Flags().Bool("dry-run", false, "Preview what would be deleted without making changes")
	cleanupCmd.Flags().Bool("cascade", false, "Recursively delete all dependent issues")
	cleanupCmd.Flags().Int("older-than", 0, "Only delete issues closed more than N days ago (0 = all closed issues)")
	cleanupCmd.Flags().Bool("ephemeral", false, "Only delete closed wisps (transient molecules)")
	// Note: cleanupCmd is added to adminCmd in admin.go
}
