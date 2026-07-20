package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

var humanCmd = &cobra.Command{
	Use:     "human",
	GroupID: "setup",
	Short:   "Show essential commands for human users",
	Long: `Display a focused help menu showing only the most common commands.

bd has 70+ commands - many for AI agents, integrations, and advanced workflows.
This command shows the ~15 essential commands that human users need most often.

For the full command list, run: bd --help

SUBCOMMANDS:
  human list              List all human-needed beads (issues with 'human' label)
  human respond <id>      Respond to a human-needed bead (adds comment and closes)
  human dismiss <id>      Dismiss a human-needed bead permanently
  human stats             Show summary statistics for human-needed beads`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("human")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		// beads-3l5q: human is a Runnable hybrid (own RunE + list/respond/
		// dismiss/stats subcommands), so the shared unknown-subcommand guard
		// skips it. A leftover positional (e.g. `bd human lst` typo of list) is
		// an unknown subcommand — reject it instead of printing the essentials
		// menu with exit 0 (silent false-success). Bare `bd human` (no args)
		// still shows the menu.
		if len(args) > 0 {
			return rejectUnknownSubcommand(cmd, args[0])
		}

		fmt.Printf("\n%s\n", ui.RenderBold("bd - Essential Commands for Humans"))
		fmt.Printf("For all 70+ commands: bd --help\n\n")

		// Issues - Core workflow
		fmt.Printf("%s\n", ui.RenderAccent("Working With Issues:"))
		printCmd("create", "Create a new issue")
		printCmd("list", "List issues (filter with --status, --priority, --label)")
		printCmd("show <id>", "Show issue details")
		printCmd("update <id>", "Update an issue (--status, --priority, --assignee)")
		printCmd("close <id>", "Close one or more issues")
		printCmd("reopen <id>", "Reopen a closed issue")
		printCmd("note <id> <text>", "Add a note to an issue (or: comments add <id>)")
		fmt.Println()

		// Workflow
		fmt.Printf("%s\n", ui.RenderAccent("Finding Work:"))
		printCmd("ready", "Show issues ready to work on (no blockers)")
		printCmd("search <query>", "Search issues by text")
		printCmd("status", "Show project overview and counts")
		printCmd("stats", "Show detailed statistics")
		fmt.Println()

		// Dependencies
		fmt.Printf("%s\n", ui.RenderAccent("Dependencies:"))
		printCmd("dep add <a> <b>", "Add dependency (a depends on b)")
		printCmd("dep remove <a> <b>", "Remove a dependency")
		printCmd("dep tree <id>", "Show dependency tree")
		printCmd("graph", "Display visual dependency graph")
		printCmd("blocked", "Show all blocked issues")
		fmt.Println()

		// Setup & Maintenance
		fmt.Printf("%s\n", ui.RenderAccent("Setup & Sync:"))
		printCmd("init", "Initialize bd in current directory")
		printCmd("sync", "Sync issues with git remote")
		printCmd("doctor", "Check installation health")
		fmt.Println()

		// Help
		fmt.Printf("%s\n", ui.RenderAccent("Getting Help:"))
		printCmd("quickstart", "Quick start guide with examples")
		printCmd("help <cmd>", "Help for any command")
		printCmd("--help", "Full command list (70+ commands)")
		fmt.Println()

		// Common examples
		fmt.Printf("%s\n", ui.RenderAccent("Quick Examples:"))
		fmt.Printf("  %s\n", ui.RenderMuted("# Create and track an issue"))
		fmt.Printf("  bd create \"Fix login bug\" --priority 1\n")
		fmt.Printf("  bd update bd-abc123 --claim\n")
		fmt.Printf("  bd close bd-abc123\n\n")

		fmt.Printf("  %s\n", ui.RenderMuted("# See what needs doing"))
		fmt.Printf("  bd ready                    # What can I work on?\n")
		fmt.Printf("  bd list --status open       # All open issues\n")
		fmt.Printf("  bd blocked                  # What's stuck?\n\n")
		return nil
	},
}

// human list command
var humanListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all human-needed beads",
	Long: `List all issues labeled with 'human' tag.

These are issues that require human intervention or input.

Examples:
  bd human list
  bd human list --status=open
  bd human list --json`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("human-list")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		status, _ := cmd.Flags().GetString("status")

		ctx := rootCtx

		filter := types.IssueFilter{
			Labels: []string{"human"},
		}

		if err := ensureStoreActive(); err != nil {
			return HandleErrorRespectJSON("listing human beads: %v", err)
		}

		if status != "" && status != "all" {
			// Validate --status the same way bd list/count/search/lint/find-duplicates
			// do: an invalid value must fail loud, not silently return an empty
			// "No human-needed beads found" (a typo'd status is a user error, not
			// "there are none"). Mirrors find_duplicates.go (beads-4blh).
			filterCfg, cfgErr := loadDirectListFilterConfig(ctx, store)
			if cfgErr != nil {
				return HandleErrorRespectJSON("%v", cfgErr)
			}
			s := types.Status(status).Normalize()
			if !s.IsValidWithCustom(filterCfg.customStatusNames()) {
				return HandleErrorRespectJSON("invalid status %q (valid: %s)", status, validStatusList(filterCfg.customStatusNames()))
			}
			filter.Status = &s
		}

		var err error
		issues, err := store.SearchIssues(ctx, "", filter)
		if err != nil {
			return HandleErrorRespectJSON("listing human beads: %v", err)
		}

		if jsonOutput {
			issueIDs := make([]string, len(issues))
			for i, issue := range issues {
				issueIDs[i] = issue.ID
			}
			labelsMap, _ := store.GetLabelsForIssues(ctx, issueIDs)
			for _, issue := range issues {
				issue.Labels = labelsMap[issue.ID]
			}

			return emitHumanListJSON(issues)
		}

		printHumanList(issues)
		return nil
	},
}

// emitHumanListJSON writes the `bd human list --json` response. beads-erw5: it
// routes through outputJSON so the response honors BD_JSON_ENVELOPE=1
// ({schema_version, data:[...]}), matching the `ready --json` control and every
// other list verb. The prior json.MarshalIndent+fmt.Println block bypassed
// outputJSON→wrapWithSchemaVersion (the lav0 MARSHAL-variant blind spot), so a
// .data consumer got a bare list even under envelope mode. Named so a
// regression at this emit site is caught by TestHumanListJSON* (which drive it).
func emitHumanListJSON(issues []*types.Issue) error {
	// beads-b2yd: a nil `[]*types.Issue` marshals to `null` (a typed-nil slice
	// still satisfies reflect.Slice in wrapWithSchemaVersion, so it is emitted
	// as-is), which breaks `.data` consumers that iterate the result. Normalize
	// an empty result to `[]` — the nil-slice ARRAY contract swept across the
	// other --json list paths (guib/4mkg/erw5 family).
	if issues == nil {
		issues = []*types.Issue{}
	}
	return outputJSON(issues)
}

func printHumanList(issues []*types.Issue) {
	if len(issues) == 0 {
		fmt.Println("No human-needed beads found.")
		return
	}

	fmt.Printf("\n%s (%d found)\n\n", ui.RenderBold("Human-needed beads"), len(issues))
	for _, issue := range issues {
		fmt.Printf("  %s %s\n", ui.RenderCommand(issue.ID), displayTitle(issue.Title))
		if issue.Status != "open" {
			fmt.Printf("    Status: %s\n", issue.Status)
		}
		if issue.Priority != 0 {
			fmt.Printf("    Priority: P%d\n", issue.Priority)
		}
		fmt.Println()
	}
}

// human respond command
var humanRespondCmd = &cobra.Command{
	Use:   "respond <issue-id>",
	Short: "Respond to a human-needed bead",
	Long: `Respond to a human-needed bead by adding a comment and closing it.

The response is added as a comment and the issue is closed with reason "Responded".

Examples:
  bd human respond bd-123 --response "Use OAuth2 for authentication"
  bd human respond bd-123 -r "Approved, proceed with implementation"`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("human-respond")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		response, _ := cmd.Flags().GetString("response")

		if response == "" {
			return HandleErrorRespectJSON("--response is required")
		}

		CheckReadonly("human respond")

		ctx := rootCtx
		issueID := args[0]

		// Hub-connected (proxied-server) crew have a nil global `store` — the
		// `human` family is in noDbCommands, so store/UOW init is skipped. Route
		// through the proxied UOW to avoid "storage is nil" (beads-ivje).
		if usesProxiedServer() {
			return runHumanRespondProxiedServer(ctx, issueID, response)
		}

		// beads-gnoav: the DIRECT/embedded path also has a nil global `store`
		// (`human` is in noDbCommands). ivje guarded only the proxied leg above;
		// without this, `bd human respond` nil-panics "storage is nil" in the
		// default embedded backend. Mirror the human list/stats siblings.
		if err := ensureStoreActive(); err != nil {
			return HandleErrorRespectJSON("responding to human bead: %v", err)
		}

		// Resolve partial ID and get issue
		result, err := resolveAndGetIssueForMutation(ctx, store, issueID)
		if err != nil {
			return HandleErrorRespectJSON("resolving issue ID %s: %v", issueID, err)
		}
		if result == nil || result.Issue == nil {
			if result != nil {
				result.Close()
			}
			return HandleErrorRespectJSON("issue not found: %s", issueID)
		}
		defer result.Close()

		resolvedID := result.ResolvedID
		issue := result.Issue
		targetStore := result.Store

		if issue.Status == "closed" {
			return HandleErrorRespectJSON("issue %s is already closed", resolvedID)
		}

		labelsMap, _ := targetStore.GetLabelsForIssues(ctx, []string{resolvedID})
		hasHumanLabel := false
		for _, label := range labelsMap[resolvedID] {
			if label == "human" {
				hasHumanLabel = true
				break
			}
		}

		if !hasHumanLabel {
			fmt.Fprintf(os.Stderr, "Warning: Issue %s does not have 'human' label\n", resolvedID)
		}

		commentText := fmt.Sprintf("Response: %s", response)
		_, err = targetStore.AddIssueComment(ctx, resolvedID, actor, commentText)
		if err != nil {
			return HandleErrorRespectJSON("adding comment: %v", err)
		}

		if err := targetStore.CloseIssue(ctx, resolvedID, "Responded", actor, ""); err != nil {
			return HandleErrorRespectJSON("closing bead: %v", err)
		}

		fmt.Printf("%s Bead %s closed with response.\n", ui.RenderPass("✔"), resolvedID)
		return nil
	},
}

// human dismiss command
var humanDismissCmd = &cobra.Command{
	Use:   "dismiss <issue-id>",
	Short: "Dismiss a human-needed bead",
	Long: `Dismiss a human-needed bead permanently without responding.

The issue is closed with a "Dismissed" reason and optional note.

Examples:
  bd human dismiss bd-123
  bd human dismiss bd-123 --reason "No longer applicable"`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("human-dismiss")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		reason, _ := cmd.Flags().GetString("reason")
		// beads-tg1js: --reason is optional with no default, so a whitespace-only
		// value collapses to no-reason (mirrors reopen/5rix3 + in93a) — else the
		// "if reason != \"\"" guard below stores "Dismissed:    " as the close
		// reason. A genuine reason is kept VERBATIM (beln6). Normalized here so
		// both the direct and proxied (runHumanDismissProxiedServer) paths agree.
		reason = normalizeOptionalReason(reason)

		CheckReadonly("human dismiss")

		ctx := rootCtx
		issueID := args[0]

		// beads-ivje: hub-connected (proxied-server) crew have a nil `store`
		// (`human` is in noDbCommands, so main.go skips both store and UOW init;
		// ensureStoreActive can't help — newDoltStoreFromConfig refuses in
		// proxied mode). Route the write through a lazily-built UOW instead.
		if usesProxiedServer() {
			return runHumanDismissProxiedServer(ctx, issueID, reason)
		}

		// beads-gnoav: the proxied caveat above is proxied-specific; on the
		// DIRECT/embedded path the global `store` is nil (`human` is in
		// noDbCommands) and ensureStoreActive is exactly the right fix (same as
		// human list/stats). Without it, `bd human dismiss` nil-panics
		// "storage is nil" in the default embedded backend.
		if err := ensureStoreActive(); err != nil {
			return HandleErrorRespectJSON("dismissing human bead: %v", err)
		}

		// Resolve partial ID and get issue
		result, err := resolveAndGetIssueForMutation(ctx, store, issueID)
		if err != nil {
			return HandleErrorRespectJSON("resolving issue ID %s: %v", issueID, err)
		}
		if result == nil || result.Issue == nil {
			if result != nil {
				result.Close()
			}
			return HandleErrorRespectJSON("issue not found: %s", issueID)
		}
		defer result.Close()

		resolvedID := result.ResolvedID
		issue := result.Issue
		targetStore := result.Store

		if issue.Status == "closed" {
			return HandleErrorRespectJSON("issue %s is already closed", resolvedID)
		}

		labelsMap, _ := targetStore.GetLabelsForIssues(ctx, []string{resolvedID})
		hasHumanLabel := false
		for _, label := range labelsMap[resolvedID] {
			if label == "human" {
				hasHumanLabel = true
				break
			}
		}

		if !hasHumanLabel {
			fmt.Fprintf(os.Stderr, "Warning: Issue %s does not have 'human' label\n", resolvedID)
		}

		closeReason := "Dismissed"
		if reason != "" {
			closeReason = fmt.Sprintf("Dismissed: %s", reason)
		}

		if err := targetStore.CloseIssue(ctx, resolvedID, closeReason, actor, ""); err != nil {
			return HandleErrorRespectJSON("closing bead: %v", err)
		}

		fmt.Printf("%s Bead %s dismissed.\n", ui.RenderPass("✔"), resolvedID)
		return nil
	},
}

// human stats command
var humanStatsCmd = &cobra.Command{
	Use:   "stats",
	Args:  cobra.NoArgs,
	Short: "Show summary statistics for human-needed beads",
	Long: `Display summary statistics for human-needed beads.

Shows counts for total, pending (open), responded (closed without dismiss),
and dismissed beads.

Example:
  bd human stats`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("human-stats")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		ctx := rootCtx

		filter := types.IssueFilter{
			Labels: []string{"human"},
		}

		if err := ensureStoreActive(); err != nil {
			return HandleErrorRespectJSON("getting human bead stats: %v", err)
		}

		var err error
		issues, err := store.SearchIssues(ctx, "", filter)
		if err != nil {
			return HandleErrorRespectJSON("getting human bead stats: %v", err)
		}

		stats := computeHumanStats(issues)
		// beads-vath: honor --json — the RunE previously called printHumanStats
		// unconditionally, so `bd human stats --json` emitted the plaintext
		// "Human Beads Stats" table with rc=0 and a script consuming --json got
		// unparseable text (the sibling `human list` already honors --json).
		if jsonOutput {
			return outputJSON(stats)
		}

		printHumanStats(stats)
		return nil
	},
}

// humanStats holds the summary counts for human-needed beads, emitted verbatim
// under `bd human stats --json` (beads-vath).
type humanStats struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Responded int `json:"responded"`
	Dismissed int `json:"dismissed"`
}

func computeHumanStats(issues []*types.Issue) humanStats {
	var s humanStats
	s.Total = len(issues)
	closed := 0

	for _, issue := range issues {
		switch issue.Status {
		case "closed":
			closed++
			if strings.Contains(strings.ToLower(issue.CloseReason), "dismiss") {
				s.Dismissed++
			}
		default:
			// All non-closed statuses (open, in_progress, blocked, hooked, etc.) are pending
			s.Pending++
		}
	}

	s.Responded = closed - s.Dismissed
	return s
}

func printHumanStats(s humanStats) {
	fmt.Printf("\n%s\n", ui.RenderBold("Human Beads Stats"))
	fmt.Println()
	fmt.Printf("  Total:      %d\n", s.Total)
	fmt.Printf("  Pending:    %d\n", s.Pending)
	fmt.Printf("  Responded:  %d\n", s.Responded)
	fmt.Printf("  Dismissed:  %d\n", s.Dismissed)
	fmt.Println()
}

// printCmd prints a command with consistent formatting
func printCmd(cmd, description string) {
	fmt.Printf("  %-20s %s\n", ui.RenderCommand(cmd), description)
}

func init() {
	// Add subcommands to humanCmd
	humanCmd.AddCommand(humanListCmd)
	humanCmd.AddCommand(humanRespondCmd)
	humanCmd.AddCommand(humanDismissCmd)
	humanCmd.AddCommand(humanStatsCmd)

	// Add flags for subcommands
	humanListCmd.Flags().StringP("status", "s", "", "Filter by status (open, closed, etc.)")
	humanRespondCmd.Flags().StringP("response", "r", "", "Response text (required)")
	_ = humanRespondCmd.MarkFlagRequired("response")
	humanDismissCmd.Flags().StringP("reason", "", "", "Reason for dismissal (optional)")

	// Register with root command
	rootCmd.AddCommand(humanCmd)
}
