package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var mergeCmd = &cobra.Command{
	Use:   "merge [source-id...] --into [target-id]",
	Short: "Merge duplicate issues into a single issue",
	Long: `Merge one or more source issues into a target issue.

This command:
1. Validates all issues exist and no self-merge
2. Closes source issues with reason 'Merged into bd-X'
3. Migrates all dependencies from sources to target
4. Updates text references in all issue descriptions/notes

Example:
  bd merge bd-42 bd-43 --into bd-42
  bd merge bd-10 bd-11 bd-12 --into bd-10 --dry-run`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetID, _ := cmd.Flags().GetString("into")
		if targetID == "" {
			fmt.Fprintf(os.Stderr, "Error: --into flag is required\n")
			os.Exit(1)
		}

		sourceIDs := args
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		// Validate merge operation
		if err := validateMerge(targetID, sourceIDs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// TODO: Add RPC support when daemon implements MergeIssues
		if daemonClient != nil {
			fmt.Fprintf(os.Stderr, "Error: merge command not yet supported in daemon mode (see bd-190)\n")
			os.Exit(1)
		}

		// Direct mode
		ctx := context.Background()

		if dryRun {
			if !jsonOutput {
				fmt.Println("Dry run - validation passed, no changes made")
				fmt.Printf("Would merge: %s into %s\n", strings.Join(sourceIDs, ", "), targetID)
			}
			return
		}

		// Perform merge
		if err := performMerge(ctx, targetID, sourceIDs); err != nil {
			fmt.Fprintf(os.Stderr, "Error performing merge: %v\n", err)
			os.Exit(1)
		}

		// Schedule auto-flush
		markDirtyAndScheduleFlush()

		if jsonOutput {
			result := map[string]interface{}{
				"target_id":  targetID,
				"source_ids": sourceIDs,
				"merged":     len(sourceIDs),
			}
			outputJSON(result)
		} else {
			green := color.New(color.FgGreen).SprintFunc()
			fmt.Printf("%s Merged %d issue(s) into %s\n", green("✓"), len(sourceIDs), targetID)
		}
	},
}

func init() {
	mergeCmd.Flags().String("into", "", "Target issue ID to merge into (required)")
	mergeCmd.Flags().Bool("dry-run", false, "Validate without making changes")
	rootCmd.AddCommand(mergeCmd)
}

// validateMerge checks that merge operation is valid
func validateMerge(targetID string, sourceIDs []string) error {
	ctx := context.Background()

	// Check target exists
	target, err := store.GetIssue(ctx, targetID)
	if err != nil {
		return fmt.Errorf("target issue not found: %s", targetID)
	}
	if target == nil {
		return fmt.Errorf("target issue not found: %s", targetID)
	}

	// Check all sources exist and validate no self-merge
	for _, sourceID := range sourceIDs {
		if sourceID == targetID {
			return fmt.Errorf("cannot merge issue into itself: %s", sourceID)
		}

		source, err := store.GetIssue(ctx, sourceID)
		if err != nil {
			return fmt.Errorf("source issue not found: %s", sourceID)
		}
		if source == nil {
			return fmt.Errorf("source issue not found: %s", sourceID)
		}
	}

	return nil
}

// performMerge executes the merge operation
func performMerge(ctx context.Context, targetID string, sourceIDs []string) error {
	// TODO: Implement actual merge logic in bd-190
	// This is a placeholder for validation purposes
	return fmt.Errorf("merge operation not yet implemented (see bd-190)")
}
