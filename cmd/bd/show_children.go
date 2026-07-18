package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/uimd"
)

// showIssueChildren displays only the children of the specified issue(s)
func showIssueChildren(ctx context.Context, args []string, jsonOut bool, shortMode bool) error {
	// Collect all children for all issues
	allChildren := make(map[string][]*types.IssueWithDependencyMetadata)

	// Process each issue to get its children
	processIssue := func(issueID string, issueStore storage.DoltStorage) error {
		// Initialize entry so "no children" message can be shown
		if _, exists := allChildren[issueID]; !exists {
			allChildren[issueID] = []*types.IssueWithDependencyMetadata{}
		}

		// Get all dependents with metadata so we can filter for children
		refs, err := issueStore.GetDependentsWithMetadata(ctx, issueID)
		if err != nil {
			return err
		}
		// Filter for only parent-child relationships
		for _, ref := range refs {
			if ref.DependencyType == types.DepParentChild {
				allChildren[issueID] = append(allChildren[issueID], ref)
			}
		}
		return nil
	}

	// Process each arg via routing-aware resolution
	failedCount := 0
	for _, id := range args {
		result, err := resolveAndGetIssueWithRouting(ctx, store, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving %s: %v\n", id, err)
			failedCount++
			continue
		}
		if result == nil || result.Issue == nil {
			if result != nil {
				result.Close()
			}
			fmt.Fprintf(os.Stderr, "Issue %s not found\n", id)
			failedCount++
			continue
		}
		if err := processIssue(result.ResolvedID, result.Store); err != nil {
			fmt.Fprintf(os.Stderr, "Error getting children for %s: %v\n", id, err)
			failedCount++
		}
		result.Close()
	}

	// Output results
	if jsonOut {
		if jerr := outputJSON(allChildren); jerr != nil {
			return jerr
		}
		// Partial/total failure: emit results (above) then signal non-zero so
		// scripts don't silently proceed on a missing id (beads-2svv).
		if failedCount > 0 {
			return &exitError{Code: 1}
		}
		return nil
	}

	// Display children
	for issueID, children := range allChildren {
		if len(children) == 0 {
			fmt.Printf("%s: No children found\n", ui.RenderAccent(issueID))
			continue
		}

		fmt.Printf("%s Children of %s (%d):\n", ui.RenderAccent("↳"), issueID, len(children))
		for _, child := range children {
			if shortMode {
				fmt.Printf("  %s\n", formatShortIssue(&child.Issue))
			} else {
				fmt.Println(formatDependencyLine("↳", child))
			}
		}
		fmt.Println()
	}
	// Found children have already been displayed; signal non-zero if any id
	// failed so `bd show --children $a $b || ...` guards fire (beads-2svv).
	if failedCount > 0 {
		return &exitError{Code: 1}
	}
	return nil
}

// showIssueAsOf displays issues as they existed at a specific commit or branch ref.
// This requires a versioned storage backend (e.g., Dolt).
func showIssueAsOf(ctx context.Context, args []string, ref string, shortMode bool) error {
	var allIssues []*types.Issue
	failedCount := 0
	for idx, id := range args {
		issue, err := store.AsOf(ctx, id, ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching %s as of %s: %v\n", id, ref, err)
			failedCount++
			continue
		}
		if issue == nil {
			fmt.Fprintf(os.Stderr, "Issue %s did not exist at %s\n", id, ref)
			failedCount++
			continue
		}

		if shortMode {
			fmt.Println(formatShortIssue(issue))
			continue
		}

		if jsonOutput {
			allIssues = append(allIssues, issue)
			continue
		}

		if idx > 0 {
			fmt.Println("\n" + ui.RenderMuted(strings.Repeat("-", 60)))
		}

		// Display header with ref indicator
		fmt.Printf("\n%s (as of %s)\n", formatIssueHeader(issue), ui.RenderMuted(ref))
		fmt.Println(formatIssueMetadata(issue))

		if issue.Description != "" {
			fmt.Printf("\n%s\n%s\n", ui.RenderBold("DESCRIPTION"), uimd.RenderMarkdown(issue.Description))
		}
		fmt.Println()
	}

	if jsonOutput && len(allIssues) > 0 {
		if jerr := outputJSON(allIssues); jerr != nil {
			return jerr
		}
	}
	// Found issues have already been printed/emitted; signal non-zero if any id
	// could not be fetched at the ref so `bd show --as-of ... || ...` guards
	// fire on a missing/typo'd id (beads-2svv).
	if failedCount > 0 {
		return &exitError{Code: 1}
	}
	return nil
}
