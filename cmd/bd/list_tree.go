package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
)

// buildIssueTree builds parent-child tree structure from issues
// Uses actual parent-child dependencies from the database when store is provided
func buildIssueTree(issues []*types.Issue) (roots []*types.Issue, childrenMap map[string][]*types.Issue) {
	return buildIssueTreeWithDeps(issues, nil)
}

// buildIssueTreeWithDeps builds parent-child tree using dependency records
// If allDeps is nil, falls back to dotted ID hierarchy (e.g., "parent.1")
// Treats any dependency on an epic as a parent-child relationship
func buildIssueTreeWithDeps(issues []*types.Issue, allDeps map[string][]*types.Dependency) (roots []*types.Issue, childrenMap map[string][]*types.Issue) {
	issueMap := make(map[string]*types.Issue)
	childrenMap = make(map[string][]*types.Issue)
	isChild := make(map[string]bool)

	// Build issue map and identify epics
	epicIDs := make(map[string]bool)
	for _, issue := range issues {
		issueMap[issue.ID] = issue
		if issue.IssueType == "epic" {
			epicIDs[issue.ID] = true
		}
	}

	// If we have dependency records, use them to find parent-child relationships
	if allDeps != nil {
		addedChild := make(map[string]bool) // tracks "parentID:childID" to prevent duplicates
		for issueID, deps := range allDeps {
			for _, dep := range deps {
				parentID := dep.DependsOnID
				// Only include if both parent and child are in the issue set
				child, childOk := issueMap[issueID]
				_, parentOk := issueMap[parentID]
				if !childOk || !parentOk {
					continue
				}

				// relates-to is a loose graph link, not a hierarchical edge:
				// treating it as parent-child causes incorrect nesting and, when
				// bidirectional, marks both endpoints as children of each other
				// — collapsing them out of the root set and silently dropping
				// whole subtrees from `bd list`. See gastownhall/beads#3936.
				if dep.Type == types.DepRelatesTo {
					continue
				}

				// Treat as parent-child if:
				// 1. Explicit parent-child dependency type, OR
				// 2. Any dependency where the target is an epic
				if dep.Type == types.DepParentChild || epicIDs[parentID] {
					key := parentID + ":" + issueID
					if !addedChild[key] {
						childrenMap[parentID] = append(childrenMap[parentID], child)
						addedChild[key] = true
					}
					isChild[issueID] = true
				}
			}
		}
	}

	// Fallback: check for hierarchical subtask IDs (e.g., "parent.1")
	for _, issue := range issues {
		if isChild[issue.ID] {
			continue // Already a child via dependency
		}
		if strings.Contains(issue.ID, ".") {
			parts := strings.Split(issue.ID, ".")
			parentID := strings.Join(parts[:len(parts)-1], ".")
			if _, exists := issueMap[parentID]; exists {
				childrenMap[parentID] = append(childrenMap[parentID], issue)
				isChild[issue.ID] = true
				continue
			}
		}
	}

	// Roots are issues that aren't children of any other issue
	for _, issue := range issues {
		if !isChild[issue.ID] {
			roots = append(roots, issue)
		}
	}

	// Sort roots for stable tree ordering (fixes unstable --tree output)
	// Use same sorting logic as children for consistency
	slices.SortFunc(roots, compareIssuesByPriority)

	// Sort children within each parent for stable ordering in data structure
	for parentID := range childrenMap {
		slices.SortFunc(childrenMap[parentID], compareIssuesByPriority)
	}

	return roots, childrenMap
}

// compareIssuesByPriority provides stable sorting for tree display
// Primary sort: priority (P0 before P1 before P2...)
// Secondary sort: ID for deterministic ordering when priorities match
func compareIssuesByPriority(a, b *types.Issue) int {
	// Primary: priority (ascending: P0 before P1 before P2...)
	if result := cmp.Compare(a.Priority, b.Priority); result != 0 {
		return result
	}
	// Secondary: ID for deterministic order when priorities match
	return utils.NaturalCompareIDs(a.ID, b.ID)
}

// printPrettyTree recursively prints the issue tree
// Children are sorted by priority (P0 first) for intuitive reading
func printPrettyTree(childrenMap map[string][]*types.Issue, parentID string, prefix string) {
	children := childrenMap[parentID]

	// Sort children by priority using same comparison as roots for consistency
	slices.SortFunc(children, compareIssuesByPriority)

	for i, child := range children {
		isLast := i == len(children)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		fmt.Printf("%s%s%s\n", prefix, connector, formatPrettyIssue(child))

		extension := "│   "
		if isLast {
			extension = "    "
		}
		printPrettyTree(childrenMap, child.ID, prefix+extension)
	}
}

// displayPrettyList displays issues in pretty tree format (GH#654)
// Uses buildIssueTree which only supports dotted ID hierarchy
func displayPrettyList(issues []*types.Issue, showHeader bool) {
	displayPrettyListWithDeps(issues, showHeader, nil)
}

// displayPrettyListWithDeps displays issues in tree format using dependency data.
// It assumes the caller passed the COMPLETE result set (no --limit truncation);
// callers that truncate must use displayPrettyListWithDepsTruncated so the
// summary footer does not falsely assert "Total".
func displayPrettyListWithDeps(issues []*types.Issue, showHeader bool, allDeps map[string][]*types.Dependency) {
	displayPrettyListWithDepsTruncated(issues, showHeader, allDeps, false)
}

// displayPrettyListWithDepsTruncated is displayPrettyListWithDeps plus a flag for
// whether the passed slice was truncated by --limit (beads-l39v). When truncated,
// the summary footer says "Showing: N issues" instead of "Total: N issues" — the
// counts are page-local, so calling them the "Total" is factually false whenever
// more rows matched than were shown (e.g. the default --limit 50 on a >50-issue
// workspace). The counts stay page-local either way (computing a ground-truth
// total would need a separate unlimited COUNT query, not available on every call
// path); relabeling stops the word from asserting completeness it doesn't have.
func displayPrettyListWithDepsTruncated(issues []*types.Issue, showHeader bool, allDeps map[string][]*types.Dependency, truncated bool) {
	displayPrettyListWithDepsContextRoot(issues, showHeader, allDeps, truncated, "")
}

// displayPrettyListWithDepsContextRoot is displayPrettyListWithDepsTruncated
// plus contextRootID: the id of an issue that was prepended to `issues` purely
// as tree-render CONTEXT (not itself a result row) and so must be excluded from
// the footer count. beads-bubp: `bd children`/`bd list --parent` prepend the
// queried parent as the tree root (list.go getHierarchicalChildren) for
// hierarchy display, but the footer counted it, so the human "Total: N issues"
// was +1 vs --json (which returns children only). Excluding contextRootID from
// the footer count (and its open/in-progress tally) makes human == json.
// Callers with no context root pass "" and are unaffected.
func displayPrettyListWithDepsContextRoot(issues []*types.Issue, showHeader bool, allDeps map[string][]*types.Dependency, truncated bool, contextRootID string) {
	if showHeader {
		// Clear screen and show header
		fmt.Print("\033[2J\033[H")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Printf("Beads - Open & In Progress (%s)\n", time.Now().Format("15:04:05"))
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println()
	}

	if len(issues) == 0 {
		fmt.Println("No issues found.")
		return
	}

	roots, childrenMap := buildIssueTreeWithDeps(issues, allDeps)

	for _, issue := range roots {
		fmt.Println(formatPrettyIssue(issue))
		printPrettyTree(childrenMap, issue.ID, "")
	}

	// Summary
	fmt.Println()
	fmt.Println(strings.Repeat("-", 80))
	openCount := 0
	inProgressCount := 0
	countedRows := 0
	for _, issue := range issues {
		// beads-bubp: skip the context-root row (the queried parent) — it is
		// displayed for hierarchy context but is not a child of itself, and
		// --json omits it, so counting it makes the footer disagree with --json.
		if contextRootID != "" && issue.ID == contextRootID {
			continue
		}
		countedRows++
		switch issue.Status {
		case "open":
			openCount++
		case "in_progress":
			inProgressCount++
		}
	}
	label := "Total"
	if truncated {
		// The slice is only a page; do not claim it is the total. The
		// stderr truncation hint (printTruncationHint) still explains how to
		// see all rows — this just keeps the stdout footer word honest.
		label = "Showing"
	}
	fmt.Printf("%s: %d issues (%d open, %d in progress)\n", label, countedRows, openCount, inProgressCount)
	fmt.Println()
	fmt.Println("Status: ○ open  ◐ in_progress  ● blocked  ✓ closed  ❄ deferred")
}
