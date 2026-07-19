package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/types"
)

// issueIDCompletion provides shell completion for issue IDs by querying the storage
// and returning a list of IDs with their titles as descriptions
func issueIDCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Initialize storage if not already initialized
	ctx := context.Background()
	if rootCtx != nil {
		ctx = rootCtx
	}

	// Get database path - use same logic as in PersistentPreRun
	currentDBPath := dbPath
	if currentDBPath == "" {
		currentDBPath = beads.FindDatabasePath()
		if currentDBPath == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}

	// Open database if store is not initialized
	currentStore := store
	if currentStore == nil {
		var err error
		currentStore, err = openReadOnlyStoreForDBPath(ctx, currentDBPath)
		if err != nil {
			// If we can't open database, return empty completion
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer func() { _ = currentStore.Close() }()
	}

	// Use SearchIssues with IDPrefix filter to efficiently query matching issues
	filter := types.IssueFilter{
		IDPrefix: toComplete, // Filter at database level for better performance
	}
	issues, err := currentStore.SearchIssues(ctx, "", filter)
	if err != nil {
		// If we can't list issues, return empty completion
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Build completion list
	completions := make([]string, 0, len(issues))
	for _, issue := range issues {
		// Format: ID\tTitle (shown during completion)
		completions = append(completions, formatIssueCompletion(issue.ID, issue.Title))
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

// formatIssueCompletion builds a cobra completion entry "ID\tTitle" where the
// Title is the description shown in the shell's completion menu. The Title is
// routed through displayTitle (beads-7n9y): a title can originate from an
// untrusted import (JSONL/markdown/SCM) carrying OSC/CSI terminal-control
// escapes (OSC 0 window-title / OSC 52 clipboard), so printing it RAW into the
// completion menu injected control sequences on <TAB>. Completion MATCHING is
// on the ID (before the tab), so sanitizing the description is safe. Sink-class
// tail of j8li/ihaw.
func formatIssueCompletion(id, title string) string {
	return fmt.Sprintf("%s\t%s", id, displayTitle(title))
}
