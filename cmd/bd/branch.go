package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/ui"
)

var branchCmd = &cobra.Command{
	Use:     "branch [name]",
	GroupID: "sync",
	Short:   "List or create branches",
	Long: `List all branches or create a new branch.

This command requires the Dolt storage backend. Without arguments,
it lists all branches. With an argument, it creates a new branch.

Examples:
  bd branch                    # List all branches
  bd branch feature-xyz        # Create a new branch named feature-xyz`,
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("branch")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		ctx := rootCtx

		// beads-jr2h4 (aocj proxied-routing class, VCS-cmd leg): in
		// proxied-server mode main.go PersistentPreRun returns before
		// newDoltStore, leaving the global `store` nil — so store.ListBranches
		// / store.Branch below nil-panicked. Branch VCS ops (list/create Dolt
		// branches) manipulate the LOCAL Dolt working set; they are
		// storage.DoltStorage version-control methods with NO proxied-UOW
		// equivalent, and the store factory deliberately refuses to open a
		// direct store in proxied config ("proxy server store should be uow
		// provider"). So — like `compact --analyze` / `config set` — this
		// operation requires direct/embedded Dolt access: fail loud with a
		// clear, purpose-built message instead of panicking.
		if usesProxiedServer() {
			return HandleErrorRespectJSON("branch operations require direct/embedded Dolt access and are not available in proxied-server mode")
		}

		// Defensive lazy-init for the direct path (aocj-class guard, mirrors
		// comments.go/cleanup.go): guarantee the store is active before the
		// version-control calls below.
		if err := ensureStoreActive(); err != nil {
			return HandleErrorWithHintRespectJSON(err.Error(), diagHint())
		}

		if len(args) == 0 {
			branches, err := store.ListBranches(ctx)
			if err != nil {
				return HandleErrorRespectJSON("failed to list branches: %v", err)
			}

			currentBranch, err := store.CurrentBranch(ctx)
			if err != nil {
				currentBranch = ""
			}

			if jsonOutput {
				return outputJSON(map[string]interface{}{
					"current":  currentBranch,
					"branches": branches,
				})
			}

			fmt.Printf("\n%s Branches:\n\n", ui.RenderAccent("🌿"))
			for _, branch := range branches {
				if branch == currentBranch {
					fmt.Printf("  * %s\n", ui.StatusInProgressStyle.Render(branch))
				} else {
					fmt.Printf("    %s\n", branch)
				}
			}
			fmt.Println()
			return nil
		}

		branchName := args[0]
		if err := store.Branch(ctx, branchName); err != nil {
			return HandleErrorRespectJSON("failed to create branch: %v", err)
		}

		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"created": branchName,
			})
		}

		fmt.Printf("Created branch: %s\n", ui.RenderAccent(branchName))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(branchCmd)
}
