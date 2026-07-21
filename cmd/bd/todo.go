package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/steveyegge/beads/internal/validation"
)

var todoCmd = &cobra.Command{
	Use:     "todo",
	GroupID: "issues",
	Short:   "Manage TODO items (convenience wrapper for task issues)",
	Long: `Manage TODO items as lightweight task issues.

TODOs are regular task-type issues with convenient shortcuts:
  bd todo add "Title"    -> bd create "Title" -t task -p 2
  bd todo                -> bd list --type task --status open
  bd todo done <id>      -> bd close <id>

TODOs can be promoted to full issues by changing type or priority:
  bd update todo-123 --type bug --priority 0`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("todo")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		// beads-jl7: a bare positional title (e.g. `bd todo "Fix the bug"`) must
		// create the todo — matching `bd create "title"` — not silently drop the
		// arg and fall through to the list view. Delegate to the add path.
		if len(args) > 0 {
			return addTodoCmd.RunE(cmd, args)
		}

		// Delegate to the shared, non-emitting list core so a single `bd todo`
		// records exactly one cli_command event ("todo"), not also "todo-list".
		return runTodoListCore(cmd, args)
	},
}

var addTodoCmd = &cobra.Command{
	Use:           "add <title>",
	Short:         "Add a new TODO item",
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("todo add")

		evt := metrics.NewCommandEvent("todo-add")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		title := strings.Join(args, " ")

		// beads-t043: parse --priority via ValidatePriority (StringP flag) so an
		// out-of-range/non-numeric value is rejected here (mirrors bd q/quick.go
		// beads-n8xi + bd create/update/list/count). Previously registered as IntP
		// and assigned directly to Priority, so `bd todo add X --priority 99` wrote
		// a bad priority silently and the canonical `P0-P4` form failed to parse.
		// Route through HandleErrorRespectJSON since bd todo add honors --json on
		// success (outputJSON(issue) below, beads-s2oy).
		priorityStr, _ := cmd.Flags().GetString("priority")
		priority, err := validation.ValidatePriority(priorityStr)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		description, _ := cmd.Flags().GetString("description")

		ctx := rootCtx

		issueType := types.TypeTask
		issue := &types.Issue{
			Title:       title,
			Description: description,
			Priority:    priority,
			IssueType:   issueType,
			Status:      types.StatusOpen,
			Assignee:    getActorWithGit(),
			Owner:       getOwner(),
			CreatedBy:   getActorWithGit(),
		}

		if err := getStore().CreateIssue(ctx, issue, getActorWithGit()); err != nil {
			// beads-j9ir: honor the --json error contract on the store-error path
			// (bd todo add --json marshals the issue on success, so a store error
			// must emit a JSON error object on stdout, not plain-text stderr).
			return HandleErrorRespectJSON("failed to create TODO: %v", err)
		}

		commandDidWrite.Store(true)

		if jsonOutput {
			// beads-s2oy: outputJSON for schema_version + BD_JSON_ENVELOPE.
			return outputJSON(issue)
		}
		fmt.Printf("Created %s: %s\n", ui.RenderID(issue.ID), displayTitle(issue.Title))
		return nil
	},
}

var listTodosCmd = &cobra.Command{
	Use:           "list",
	Args:          cobra.NoArgs, // beads-8jy7e: reject stray positionals with a clean usage error
	Short:         "List TODO items",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("todo-list")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		return runTodoListCore(cmd, args)
	},
}

// runTodoListCore lists TODO (task) issues. It deliberately emits no metrics
// event so callers own event emission: `bd todo list` emits "todo-list" and the
// bare `bd todo` alias emits "todo", each exactly once.
func runTodoListCore(cmd *cobra.Command, _ []string) error {
	showAll, _ := cmd.Flags().GetBool("all")

	ctx := rootCtx

	taskType := types.TypeTask
	filter := types.IssueFilter{
		IssueType: &taskType,
	}
	if !showAll {
		openStatus := types.StatusOpen
		filter.Status = &openStatus
	}

	issues, err := getStore().SearchIssues(ctx, "", filter)
	if err != nil {
		// beads-j9ir: honor the --json error contract on the store-error path
		// (bd todo / bd todo list --json marshals the list on success).
		return HandleErrorRespectJSON("failed to list TODOs: %v", err)
	}

	if jsonOutput {
		// beads-s2oy: outputJSON for schema_version + BD_JSON_ENVELOPE.
		return outputJSON(issues)
	}
	if len(issues) == 0 {
		fmt.Println("No TODOs found")
		return nil
	}

	todoSortIssues(issues)

	for _, issue := range issues {
		statusIcon := ui.RenderStatusIcon(string(issue.Status))
		priority := ui.RenderPriority(issue.Priority)
		fmt.Printf("  %s %s  %-40s  %s  %s\n",
			statusIcon,
			ui.RenderID(issue.ID),
			todoTruncate(issue.Title, 40),
			priority,
			issue.Status)
	}
	fmt.Printf("\nTotal: %d TODOs\n", len(issues))
	return nil
}

// todoDoneReasonOrDefault returns the close reason for `bd todo done`, falling
// through to the "Completed" default when no reason was provided. A
// whitespace-only --reason is treated as NOT provided (beads-07sko): the plain
// `reason == ""` guard let a whitespace value through, so it was stored as the
// issue's close_reason instead of the default — the override-a-default axis of
// the stored-blank-reason class (mirrors bd close's TrimSpace guard in93a and
// bd mol squash --summary au0rt). A genuine reason is used VERBATIM (its
// formatting preserved).
func todoDoneReasonOrDefault(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "Completed"
	}
	return reason
}

var doneTodoCmd = &cobra.Command{
	Use:           "done <id> [<id>...]",
	Short:         "Mark TODO(s) as done",
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("todo done")

		evt := metrics.NewCommandEvent("todo-done")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		ctx := rootCtx

		reason, _ := cmd.Flags().GetString("reason")
		reason = todoDoneReasonOrDefault(reason)

		var closedIDs []string
		failedCount := 0
		for _, issueID := range args {
			issue, err := getStore().GetIssue(ctx, issueID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to get issue %s: %v\n", issueID, err)
				failedCount++
				continue
			}
			if issue == nil {
				fmt.Fprintf(os.Stderr, "Error: issue %s not found\n", issueID)
				failedCount++
				continue
			}

			if err := getStore().CloseIssue(ctx, issueID, reason, getActorWithGit(), ""); err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to close %s: %v\n", issueID, err)
				failedCount++
				continue
			}
			closedIDs = append(closedIDs, issueID)
		}

		if len(closedIDs) > 0 {
			commandDidWrite.Store(true)
		}

		if jsonOutput {
			// beads-s2oy: outputJSON for schema_version + BD_JSON_ENVELOPE.
			if err := outputJSON(map[string]interface{}{
				"closed": closedIDs,
				"reason": reason,
			}); err != nil {
				return err
			}
			// Closed set already emitted; signal non-zero if any id failed so
			// `bd todo done $ids || ...` guards fire on a missing/typo'd id
			// rather than silently proceeding when nothing was closed (beads-xi35).
			if failedCount > 0 {
				return &exitError{Code: 1}
			}
			return nil
		}
		for _, id := range closedIDs {
			fmt.Printf("Closed %s\n", ui.RenderID(id))
		}
		// Closed set already displayed; signal non-zero on any per-id failure so
		// scripts don't proceed on a partial/total close failure (beads-xi35).
		if failedCount > 0 {
			return &exitError{Code: 1}
		}
		return nil
	},
}

func init() {
	// Add subcommands
	todoCmd.AddCommand(addTodoCmd)
	todoCmd.AddCommand(listTodosCmd)
	todoCmd.AddCommand(doneTodoCmd)

	// Add flags
	// beads-t043: StringP (0-4 or P0-P4) via the shared helper, validated in RunE
	// by validation.ValidatePriority — matches bd q/create/update/list/count.
	registerPriorityFlag(addTodoCmd, "2")
	addTodoCmd.Flags().StringP("description", "d", "", "Description")

	listTodosCmd.Flags().Bool("all", false, "Show all TODOs including completed")

	doneTodoCmd.Flags().String("reason", "", "Reason for closing (default: Completed)")

	// Register with root
	rootCmd.AddCommand(todoCmd)
}

// todoTruncate truncates a string to the specified length with ellipsis
func todoTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// todoSortIssues sorts issues by priority (ascending) then ID
func todoSortIssues(issues []*types.Issue) {
	slices.SortFunc(issues, func(a, b *types.Issue) int {
		if a.Priority != b.Priority {
			return a.Priority - b.Priority
		}
		return utils.NaturalCompareIDs(a.ID, b.ID)
	})
}
