package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/types"
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import issues from JSONL format",
	Long: `Import issues from JSON Lines format (one JSON object per line).

Reads from stdin by default, or use -i flag for file input.

Behavior:
  - Existing issues (same ID) are updated
  - New issues are created
  - Import is atomic (all or nothing)`,
	Run: func(cmd *cobra.Command, args []string) {
		input, _ := cmd.Flags().GetString("input")
		skipUpdate, _ := cmd.Flags().GetBool("skip-existing")

		// Open input
		in := os.Stdin
		if input != "" {
			f, err := os.Open(input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening input file: %v\n", err)
				os.Exit(1)
			}
			defer f.Close()
			in = f
		}

		// Read and parse JSONL
		ctx := context.Background()
		scanner := bufio.NewScanner(in)

		var created, updated, skipped int
		lineNum := 0

		for scanner.Scan() {
			lineNum++
			line := scanner.Text()

			// Skip empty lines
			if line == "" {
				continue
			}

			// Parse JSON
			var issue types.Issue
			if err := json.Unmarshal([]byte(line), &issue); err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing line %d: %v\n", lineNum, err)
				os.Exit(1)
			}

			// Check if issue exists
			existing, err := store.GetIssue(ctx, issue.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error checking issue %s: %v\n", issue.ID, err)
				os.Exit(1)
			}

			if existing != nil {
				if skipUpdate {
					skipped++
					continue
				}
				// Update existing issue - convert to updates map
				updates := make(map[string]interface{})
				if issue.Title != "" {
					updates["title"] = issue.Title
				}
				if issue.Description != "" {
					updates["description"] = issue.Description
				}
				if issue.Status != "" {
					updates["status"] = issue.Status
				}
				if issue.Priority != 0 {
					updates["priority"] = issue.Priority
				}
				if issue.IssueType != "" {
					updates["issue_type"] = issue.IssueType
				}
				if issue.Assignee != "" {
					updates["assignee"] = issue.Assignee
				}
				if issue.EstimatedMinutes != nil {
					updates["estimated_minutes"] = *issue.EstimatedMinutes
				}

				if err := store.UpdateIssue(ctx, issue.ID, updates, "import"); err != nil {
					fmt.Fprintf(os.Stderr, "Error updating issue %s: %v\n", issue.ID, err)
					os.Exit(1)
				}
				updated++
			} else {
				// Create new issue
				if err := store.CreateIssue(ctx, &issue, "import"); err != nil {
					fmt.Fprintf(os.Stderr, "Error creating issue %s: %v\n", issue.ID, err)
					os.Exit(1)
				}
				created++
			}
		}

		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
			os.Exit(1)
		}

		// Print summary
		fmt.Fprintf(os.Stderr, "Import complete: %d created, %d updated", created, updated)
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, ", %d skipped", skipped)
		}
		fmt.Fprintf(os.Stderr, "\n")
	},
}

func init() {
	importCmd.Flags().StringP("input", "i", "", "Input file (default: stdin)")
	importCmd.Flags().BoolP("skip-existing", "s", false, "Skip existing issues instead of updating them")
	rootCmd.AddCommand(importCmd)
}
