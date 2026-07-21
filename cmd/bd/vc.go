package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/ui"
)

var vcCmd = &cobra.Command{
	Use:     "vc",
	GroupID: "sync",
	Short:   "Version control operations",
	Long: `Version control operations for the beads database.

These commands provide git-like version control for your issue data, including branching, merging, and
viewing history.

Note: 'bd history', 'bd diff', and 'bd branch' also work for quick access.
This subcommand provides additional operations like merge and commit.`,
}

// requireDirectVC fails loud when running against a proxied server.
//
// beads-6iwwf (aocj proxied-routing class, VCS/data-plane leg): vc is NOT in
// noDbCommands and every subcommand (merge/commit/status) calls store.Merge /
// store.Commit / store.CommitMergeResolution / store.ResolveConflicts /
// store.GetCurrentCommit / store.CurrentBranch / store.Status directly. In
// proxied-server mode main.go PersistentPreRun returns before newDoltStore,
// leaving the global `store` nil — so those calls nil-panicked (sibling of the
// beads-jr2h4 branch leg and beads-i2v77 merge-slot leg). There is no
// proxied/UOW version-control path, and the store factory refuses to open a
// direct store in proxied config, so — like `bd branch` / `bd merge-slot` /
// `compact --analyze` — vc requires direct/embedded Dolt access: fail loud with
// a clear, --json-contract-correct message instead of panicking.
func requireDirectVC() error {
	if usesProxiedServer() {
		return HandleErrorRespectJSON("version-control operations require direct/embedded Dolt access and are not available in proxied-server mode")
	}
	// Defensive lazy-init for the direct path (mirrors branch.go/merge_slot.go):
	// guarantee the store is active before the version-control calls below.
	if err := ensureStoreActive(); err != nil {
		return HandleErrorWithHintRespectJSON(err.Error(), diagHint())
	}
	return nil
}

var vcMergeStrategy string

var vcMergeCmd = &cobra.Command{
	Use:   "merge <branch>",
	Short: "Merge a branch into the current branch",
	Long: `Merge the specified branch into the current branch.

If there are merge conflicts, they will be reported. You can resolve
conflicts with --strategy.

Examples:
  bd vc merge feature-xyz                    # Merge feature-xyz into current branch
  bd vc merge feature-xyz --strategy ours    # Merge, preferring our changes on conflict
  bd vc merge feature-xyz --strategy theirs  # Merge, preferring their changes on conflict`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// beads-q634: vc merge is a data-plane write (performs a Dolt merge) —
		// honor the --readonly sandbox like vc commit, not just commit. Found
		// scouting the vc family after guarding commit; merge was a sibling
		// bypass not named in the original q634 list (config/import/vc-commit).
		CheckReadonly("vc merge")
		evt := metrics.NewCommandEvent("vc-merge")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if err := requireDirectVC(); err != nil {
			return err
		}

		ctx := rootCtx
		branchName := args[0]

		// beads-71jpi (EARLY-VALIDATION-PARITY with bd federation sync): validate
		// --strategy UP-FRONT, before store.Merge runs. Otherwise an invalid value
		// (e.g. the typo 'our') is silently accepted on a clean merge — never
		// consulted, prints "Successfully merged" RC=0 — and on a conflicting merge
		// fails only LATE in store.ResolveConflicts, after Merge already mutated the
		// working set. federation.go rejects a bad strategy RC!=0 before any work;
		// mirror that guard here so both commands sharing this flag behave alike.
		if vcMergeStrategy != "" && vcMergeStrategy != "ours" && vcMergeStrategy != "theirs" {
			return HandleErrorRespectJSON("invalid strategy %q: must be 'ours' or 'theirs'", vcMergeStrategy)
		}

		// Pre-merge HEAD scopes the post-resolution is_blocked recompute
		// (bd-578h9.11); empty degrades to a full-graph pass.
		preHead, _ := store.GetCurrentCommit(ctx)

		// Perform merge
		conflicts, err := store.Merge(ctx, branchName)
		if err != nil {
			return HandleErrorRespectJSON("failed to merge branch: %v", err)
		}

		if len(conflicts) > 0 {
			if vcMergeStrategy != "" {
				for _, conflict := range conflicts {
					table := conflict.Field
					if table == "" {
						table = "issues"
					}
					if err := store.ResolveConflicts(ctx, table, vcMergeStrategy); err != nil {
						return HandleErrorRespectJSON("failed to resolve conflicts: %v", err)
					}
				}
				// Conclude the merge: an unresolved-then-resolved working set
				// stays uncommitted otherwise, and the merged-in writes
				// bypassed every is_blocked hook (bd-578h9.11). Use
				// CommitMergeResolution, not Commit: server-mode Commit excludes
				// config (GH#2455), so a resolved config conflict — routine now
				// that kv.* user data syncs through config — would be silently
				// dropped, leaving the merge unconcluded and re-wedging the next
				// pull/sync (GH#2474).
				if err := store.CommitMergeResolution(ctx, fmt.Sprintf("Resolve merge conflicts from %s using %s strategy", branchName, vcMergeStrategy)); err != nil {
					return HandleErrorRespectJSON("conflicts resolved but commit failed: %v", err)
				}
				if rs, ok := store.(interface {
					RecomputeBlockedAfterMerge(ctx context.Context, fromCommit string) error
				}); ok {
					if err := rs.RecomputeBlockedAfterMerge(ctx, preHead); err != nil {
						return HandleErrorRespectJSON("conflicts resolved but is_blocked recompute failed: %v", err)
					}
				}
				if jsonOutput {
					return outputJSON(buildMergeJSON(branchName, conflicts, vcMergeStrategy))
				}
				fmt.Printf("Merged %s with %d conflicts resolved using '%s' strategy\n",
					ui.RenderAccent(branchName), len(conflicts), vcMergeStrategy)
				return nil
			}

			if jsonOutput {
				return outputJSON(buildMergeJSON(branchName, conflicts, ""))
			}

			fmt.Printf("\n%s Merge completed with conflicts:\n\n", ui.RenderAccent("!!"))
			for _, conflict := range conflicts {
				fmt.Printf("  - %s\n", conflict.Field)
			}
			fmt.Printf("\nResolve conflicts with: bd vc merge %s --strategy [ours|theirs]\n\n", branchName)
			return nil
		}

		if jsonOutput {
			return outputJSON(buildMergeJSON(branchName, conflicts, ""))
		}

		fmt.Printf("Successfully merged %s\n", ui.RenderAccent(branchName))
		return nil
	},
}

// buildMergeJSON assembles the stable-shape JSON payload for `bd vc merge --json`
// across all three outcome legs (clean / auto-resolved / unresolved), fixing the
// beads-a3et0 shape-instability:
//   - "conflicts" is ALWAYS a JSON array of conflict objects (empty [] on a clean
//     merge, the set on the resolved/unresolved legs) — never a bare int on some
//     legs and an array on others (the gf0o8-tier same-key int-vs-array flip that
//     broke a consumer doing len(.conflicts) or .conflicts[0]).
//   - "conflict_count" is a stable int the caller can rely on regardless of outcome.
//   - "resolved_with" is ALWAYS present (the strategy string when the merge was
//     auto-resolved, else null) — never a key that only appears on one leg.
//
// resolvedWith is "" when the merge was not auto-resolved with a strategy.
func buildMergeJSON(branchName string, conflicts []storage.Conflict, resolvedWith string) map[string]interface{} {
	// Normalize nil->[] so the array marshals as [] (not null) on the clean leg.
	if conflicts == nil {
		conflicts = []storage.Conflict{}
	}
	var rw interface{}
	if resolvedWith != "" {
		rw = resolvedWith
	}
	return map[string]interface{}{
		"merged":         branchName,
		"conflicts":      conflicts,
		"conflict_count": len(conflicts),
		"resolved_with":  rw,
	}
}

var vcCommitMessage string
var vcCommitStdin bool

var vcCommitCmd = &cobra.Command{
	Use:   "commit",
	Args:  cobra.NoArgs, // beads-8jy7e: reject stray positionals with a clean usage error
	Short: "Create a commit with all staged changes",
	Long: `Create a new Dolt commit with all current changes.

Examples:
  bd vc commit -m "Added new feature issues"
  bd vc commit --message "Fixed priority on several issues"
  echo "Multi-line message" | bd vc commit --stdin`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// beads-q634: vc commit is a data-plane write path — honor the --readonly
		// sandbox rather than letting a sandboxed run touch the commit path.
		CheckReadonly("vc commit")
		evt := metrics.NewCommandEvent("vc-commit")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if err := requireDirectVC(); err != nil {
			return err
		}

		ctx := rootCtx

		if vcCommitStdin {
			if vcCommitMessage != "" {
				return HandleErrorRespectJSON("cannot specify both --stdin and -m/--message")
			}
			b, err := readAllLimited(os.Stdin, "commit message")
			if err != nil {
				return HandleErrorRespectJSON("failed to read commit message from stdin: %v", err)
			}
			vcCommitMessage = strings.TrimRight(string(b), "\n")
		}

		if vcCommitMessage == "" {
			return HandleErrorRespectJSON("commit message is required (use -m, --message, or --stdin)")
		}

		commandDidExplicitDoltCommit = true
		if err := store.Commit(ctx, vcCommitMessage); err != nil {
			if isDoltNothingToCommit(err) {
				if jsonOutput {
					// beads-a3et0: always include "hash" so the key set is stable
					// across the committed/nothing-to-commit legs (avoid omitempty,
					// which would re-introduce the 8qf2q key-set flip).
					return outputJSON(map[string]interface{}{"committed": false, "hash": "", "message": "nothing to commit"})
				}
				fmt.Println("Nothing to commit")
				return nil
			}
			return HandleErrorRespectJSON("failed to commit: %v", err)
		}

		hash, err := store.GetCurrentCommit(ctx)
		if err != nil {
			hash = "(unknown)"
		}

		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"committed": true,
				"hash":      hash,
				"message":   vcCommitMessage,
			})
		}

		fmt.Printf("Created commit %s\n", ui.RenderMuted(hash[:8]))
		return nil
	},
}

var vcStatusCmd = &cobra.Command{
	Use:   "status",
	Args:  cobra.NoArgs, // beads-8jy7e: reject stray positionals with a clean usage error
	Short: "Show current branch and uncommitted changes",
	Long: `Show the current branch, commit hash, and any uncommitted changes.

Examples:
  bd vc status`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("vc-status")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if err := requireDirectVC(); err != nil {
			return err
		}

		ctx := rootCtx

		currentBranch, err := store.CurrentBranch(ctx)
		if err != nil {
			return HandleErrorRespectJSON("failed to get current branch: %v", err)
		}

		currentCommit, err := store.GetCurrentCommit(ctx)
		if err != nil {
			currentCommit = "(unknown)"
		}

		// beads-xz6e: the help promises "uncommitted changes" but the command
		// never reported them. Query the working-set status (dolt_status via the
		// existing Status accessor) so the human + JSON paths honor the help. A
		// Status error is non-fatal — still show branch/commit, just omit the
		// uncommitted detail (best-effort, matches GetCurrentCommit's leniency).
		var staged, unstaged []storage.StatusEntry
		if st, serr := store.Status(ctx); serr == nil && st != nil {
			staged, unstaged = st.Staged, st.Unstaged
		}
		uncommitted := len(staged)+len(unstaged) > 0

		if jsonOutput {
			changedTables := make([]string, 0, len(staged)+len(unstaged))
			for _, e := range staged {
				changedTables = append(changedTables, e.Table)
			}
			for _, e := range unstaged {
				changedTables = append(changedTables, e.Table)
			}
			return outputJSON(map[string]interface{}{
				"branch":         currentBranch,
				"commit":         currentCommit,
				"uncommitted":    uncommitted,
				"changed_tables": changedTables,
			})
		}

		fmt.Printf("\n%s Version Control Status\n\n", ui.RenderAccent("📊"))
		fmt.Printf("  Branch: %s\n", ui.StatusInProgressStyle.Render(currentBranch))
		fmt.Printf("  Commit: %s\n", ui.RenderMuted(currentCommit[:8]))
		if uncommitted {
			fmt.Printf("  Uncommitted changes: %d table(s)\n", len(staged)+len(unstaged))
			for _, e := range staged {
				fmt.Printf("    %s %s (staged)\n", ui.RenderWarn("●"), e.Table)
			}
			for _, e := range unstaged {
				fmt.Printf("    %s %s\n", ui.RenderWarn("●"), e.Table)
			}
		} else {
			fmt.Printf("  Uncommitted changes: %s\n", ui.RenderMuted("none"))
		}
		fmt.Println()
		return nil
	},
}

func init() {
	vcMergeCmd.Flags().StringVar(&vcMergeStrategy, "strategy", "", "Conflict resolution strategy: 'ours' or 'theirs'")
	vcCommitCmd.Flags().StringVarP(&vcCommitMessage, "message", "m", "", "Commit message")
	vcCommitCmd.Flags().BoolVar(&vcCommitStdin, "stdin", false, "Read commit message from stdin")

	vcCmd.AddCommand(vcMergeCmd)
	vcCmd.AddCommand(vcCommitCmd)
	vcCmd.AddCommand(vcStatusCmd)
	rootCmd.AddCommand(vcCmd)
}
