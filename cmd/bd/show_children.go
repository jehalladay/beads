package main

import (
	"context"
	"fmt"
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

	// Process each arg via routing-aware resolution.
	//
	// beads-yj1n2: per-item failures must honor the --json error contract that
	// the main `bd show` loop uses (beads-fg6/92tz/8lqh). Under --json a bare
	// plain-text stderr line is not machine-parseable, and pairing an empty
	// stdout payload with a stderr error recreates the 92tz "two objects on
	// 2>&1" break. So under --json we DEFER per-item errors and flush them to
	// stderr as JSON objects only on PARTIAL success (some id resolved, so
	// stdout carries a payload); when nothing resolved, a single stdout JSON
	// error object is the sole output and stderr stays clean. Non-JSON prints
	// immediately (unchanged).
	failedCount := 0
	foundCount := 0
	var deferredItemErrors []string
	reportShowItemError := func(format string, a ...interface{}) {
		if jsonOut {
			deferredItemErrors = append(deferredItemErrors, fmt.Sprintf(format, a...))
			return
		}
		reportItemError(format, a...)
	}
	for _, id := range args {
		result, err := resolveAndGetIssueWithRouting(ctx, store, id)
		if err != nil {
			reportShowItemError("Error resolving %s: %v", id, err)
			failedCount++
			continue
		}
		if result == nil || result.Issue == nil {
			if result != nil {
				result.Close()
			}
			reportShowItemError("Issue %s not found", id)
			failedCount++
			continue
		}
		if err := processIssue(result.ResolvedID, result.Store); err != nil {
			reportShowItemError("Error getting children for %s: %v", id, err)
			failedCount++
			result.Close()
			continue
		}
		foundCount++
		result.Close()
	}

	// Output results
	if jsonOut {
		if foundCount > 0 {
			// Partial success: stdout carries the resolved children; flush any
			// per-item failures to stderr as JSON objects (fg6 contract).
			for _, msg := range deferredItemErrors {
				reportItemError("%s", msg)
			}
			if jerr := outputJSON(allChildren); jerr != nil {
				return jerr
			}
			// Signal non-zero so scripts don't silently proceed (beads-2svv).
			if failedCount > 0 {
				return &exitError{Code: 1}
			}
			return nil
		}
		// Nothing resolved: the single stdout JSON error object is the sole
		// error output; stderr stays clean so a 2>&1 consumer gets exactly one
		// JSON object (beads-92tz).
		return HandleErrorRespectJSON("no issues found matching the provided IDs")
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
	// beads-yj1n2: same --json per-item error contract as showIssueChildren /
	// the main bd show loop (fg6/92tz/8lqh). Defer under --json; flush to
	// stderr as JSON only on partial success; a single stdout JSON error when
	// nothing was fetched.
	var deferredItemErrors []string
	reportAsOfItemError := func(format string, a ...interface{}) {
		if jsonOutput {
			deferredItemErrors = append(deferredItemErrors, fmt.Sprintf(format, a...))
			return
		}
		reportItemError(format, a...)
	}
	for idx, id := range args {
		issue, err := store.AsOf(ctx, id, ref)
		if err != nil {
			reportAsOfItemError("Error fetching %s as of %s: %v", id, ref, err)
			failedCount++
			continue
		}
		if issue == nil {
			reportAsOfItemError("Issue %s did not exist at %s", id, ref)
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

	if jsonOutput {
		if len(allIssues) > 0 {
			// Partial success: stdout carries the fetched issues; flush any
			// per-item failures to stderr as JSON objects (fg6 contract).
			for _, msg := range deferredItemErrors {
				reportItemError("%s", msg)
			}
			if jerr := outputJSON(allIssues); jerr != nil {
				return jerr
			}
			// Signal non-zero if any id failed (beads-2svv).
			if failedCount > 0 {
				return &exitError{Code: 1}
			}
			return nil
		}
		// Nothing fetched: single stdout JSON error object, clean stderr
		// (beads-92tz). shortMode carries no JSON payload, so this also covers
		// the all-failed shortMode case.
		if failedCount > 0 {
			return HandleErrorRespectJSON("no issues found matching the provided IDs at %s", ref)
		}
		return nil
	}
	// Found issues have already been printed; signal non-zero if any id could
	// not be fetched at the ref so `bd show --as-of ... || ...` guards fire on
	// a missing/typo'd id (beads-2svv).
	if failedCount > 0 {
		return &exitError{Code: 1}
	}
	return nil
}
