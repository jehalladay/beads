package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/types"
)

var epicCmd = &cobra.Command{
	Use:   "epic",
	Short: "Epic management commands",
}

var epicStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show epic completion status",
	Run: func(cmd *cobra.Command, args []string) {
		eligibleOnly, _ := cmd.Flags().GetBool("eligible-only")
		jsonOutput, _ := cmd.Flags().GetBool("json")

		// TODO: Add RPC support when daemon is running
		if daemonClient != nil {
			fmt.Fprintf(os.Stderr, "Error: epic commands not yet supported in daemon mode\n")
			fmt.Fprintf(os.Stderr, "Hint: use --no-daemon flag for direct mode\n")
			os.Exit(1)
		}

		ctx := context.Background()
		epics, err := store.GetEpicsEligibleForClosure(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting epic status: %v\n", err)
			os.Exit(1)
		}

		// Filter if eligible-only flag is set
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
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(epics); err != nil {
				fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
				os.Exit(1)
			}
			return
		}

		// Human-readable output
		if len(epics) == 0 {
			fmt.Println("No open epics found")
			return
		}

		cyan := color.New(color.FgCyan).SprintFunc()
		yellow := color.New(color.FgYellow).SprintFunc()
		green := color.New(color.FgGreen).SprintFunc()
		bold := color.New(color.Bold).SprintFunc()

		for _, epicStatus := range epics {
			epic := epicStatus.Epic
			percentage := 0
			if epicStatus.TotalChildren > 0 {
				percentage = (epicStatus.ClosedChildren * 100) / epicStatus.TotalChildren
			}

			statusIcon := ""
			if epicStatus.EligibleForClose {
				statusIcon = green("✓")
			} else if percentage > 0 {
				statusIcon = yellow("○")
			} else {
				statusIcon = "○"
			}

			fmt.Printf("%s %s %s\n", statusIcon, cyan(epic.ID), bold(epic.Title))
			fmt.Printf("   Progress: %d/%d children closed (%d%%)\n",
				epicStatus.ClosedChildren, epicStatus.TotalChildren, percentage)
			if epicStatus.EligibleForClose {
				fmt.Printf("   %s\n", green("Eligible for closure"))
			}
			fmt.Println()
		}
	},
}

var closeEligibleEpicsCmd = &cobra.Command{
	Use:   "close-eligible",
	Short: "Close epics where all children are complete",
	Run: func(cmd *cobra.Command, args []string) {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		jsonOutput, _ := cmd.Flags().GetBool("json")

		// TODO: Add RPC support when daemon is running
		if daemonClient != nil {
			fmt.Fprintf(os.Stderr, "Error: epic commands not yet supported in daemon mode\n")
			fmt.Fprintf(os.Stderr, "Hint: use --no-daemon flag for direct mode\n")
			os.Exit(1)
		}

		ctx := context.Background()
		epics, err := store.GetEpicsEligibleForClosure(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting eligible epics: %v\n", err)
			os.Exit(1)
		}

		// Filter to only eligible ones
		eligibleEpics := []*types.EpicStatus{}
		for _, epic := range epics {
			if epic.EligibleForClose {
				eligibleEpics = append(eligibleEpics, epic)
			}
		}

		if len(eligibleEpics) == 0 {
			if !jsonOutput {
				fmt.Println("No epics eligible for closure")
			} else {
				fmt.Println("[]")
			}
			return
		}

		if dryRun {
			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(eligibleEpics); err != nil {
					fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Printf("Would close %d epic(s):\n", len(eligibleEpics))
				for _, epicStatus := range eligibleEpics {
					fmt.Printf("  - %s: %s\n", epicStatus.Epic.ID, epicStatus.Epic.Title)
				}
			}
			return
		}

		// Actually close the epics
		closedIDs := []string{}
		for _, epicStatus := range eligibleEpics {
			err := store.CloseIssue(ctx, epicStatus.Epic.ID, "All children completed", "system")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error closing %s: %v\n", epicStatus.Epic.ID, err)
				continue
			}
			closedIDs = append(closedIDs, epicStatus.Epic.ID)
		}

		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(map[string]interface{}{
				"closed": closedIDs,
				"count":  len(closedIDs),
			}); err != nil {
				fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("✓ Closed %d epic(s)\n", len(closedIDs))
			for _, id := range closedIDs {
				fmt.Printf("  - %s\n", id)
			}
		}
	},
}

func init() {
	epicCmd.AddCommand(epicStatusCmd)
	epicCmd.AddCommand(closeEligibleEpicsCmd)

	epicStatusCmd.Flags().Bool("eligible-only", false, "Show only epics eligible for closure")
	epicStatusCmd.Flags().Bool("json", false, "Output in JSON format")

	closeEligibleEpicsCmd.Flags().Bool("dry-run", false, "Preview what would be closed without making changes")
	closeEligibleEpicsCmd.Flags().Bool("json", false, "Output in JSON format")

	rootCmd.AddCommand(epicCmd)
}
