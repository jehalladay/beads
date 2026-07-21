package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// showIssueRefs displays issues that reference the given issue(s), grouped by relationship type
func showIssueRefs(ctx context.Context, args []string, jsonOut bool) error {
	// Collect all refs for all issues
	allRefs := make(map[string][]*types.IssueWithDependencyMetadata)

	// Process each issue
	processIssue := func(issueID string, issueStore storage.DoltStorage) error {
		refs, err := issueStore.GetDependentsWithMetadata(ctx, issueID)
		if err != nil {
			return err
		}
		// beads-d995: GetDependentsWithMetadata returns a nil slice when the issue
		// has no dependents (both the embedded and dolt stores). A nil slice stored
		// as a map value marshals to JSON null, so `bd show <id> --refs --json` on
		// the common no-refs case emits {"<id>":null,...} while an issue WITH a
		// dependent emits an array — forcing consumers to special-case null vs [].
		// Init to an empty slice so the array contract holds for both cases (the
		// nil-slice→null class, siblings 5fv3/036h/guib; map-value variant). The
		// text path below only ranges/lens refs, so [] is behaviorally identical.
		if refs == nil {
			refs = []*types.IssueWithDependencyMetadata{}
		}
		allRefs[issueID] = refs
		return nil
	}

	// Process each arg via routing-aware resolution.
	//
	// beads-0rlll (yj1n2 sibling): per-item failures must honor the --json error
	// contract the main `bd show` loop uses (beads-fg6/92tz/8lqh). Under --json a
	// bare plain-text stderr line is not machine-parseable, and pairing an empty
	// stdout payload with a stderr error recreates the 92tz "two objects on 2>&1"
	// break. So under --json we DEFER per-item errors and flush them to stderr as
	// JSON objects only on PARTIAL success (some id resolved, so stdout carries a
	// payload); when nothing resolved, a single stdout JSON error object is the
	// sole output and stderr stays clean. Non-JSON prints immediately (unchanged).
	// Mirrors showIssueChildren/showIssueAsOf (show_children.go).
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
			reportShowItemError("Error getting refs for %s: %v", id, err)
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
			// Partial success: stdout carries the resolved refs; flush any
			// per-item failures to stderr as JSON objects (fg6 contract).
			for _, msg := range deferredItemErrors {
				reportItemError("%s", msg)
			}
			if jerr := outputJSON(allRefs); jerr != nil {
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

	// Display refs grouped by issue and relationship type
	for issueID, refs := range allRefs {
		if len(refs) == 0 {
			fmt.Printf("\n%s: No references found\n", ui.RenderAccent(issueID))
			continue
		}

		fmt.Printf("\n%s References to %s:\n", ui.RenderAccent("📎"), issueID)

		// Group refs by type
		refsByType := make(map[types.DependencyType][]*types.IssueWithDependencyMetadata)
		for _, ref := range refs {
			refsByType[ref.DependencyType] = append(refsByType[ref.DependencyType], ref)
		}

		// Display each type
		typeOrder := []types.DependencyType{
			types.DepUntil, types.DepCausedBy, types.DepValidates,
			types.DepBlocks, types.DepParentChild, types.DepRelatesTo,
			types.DepTracks, types.DepDiscoveredFrom, types.DepRelated,
			types.DepSupersedes, types.DepDuplicates, types.DepRepliesTo,
			types.DepApprovedBy, types.DepAuthoredBy, types.DepAssignedTo,
		}

		// First show types in order, then any others
		shown := make(map[types.DependencyType]bool)
		for _, depType := range typeOrder {
			if refs, ok := refsByType[depType]; ok {
				displayRefGroup(depType, refs)
				shown[depType] = true
			}
		}
		// Show any remaining types
		for depType, refs := range refsByType {
			if !shown[depType] {
				displayRefGroup(depType, refs)
			}
		}
		fmt.Println()
	}
	// Found refs have already been displayed; signal non-zero if any id failed
	// so `bd show --refs $a $b || ...` guards fire on a missing id (beads-2svv).
	if failedCount > 0 {
		return &exitError{Code: 1}
	}
	return nil
}

// displayRefGroup displays a group of references with a given type
// Closed items get entire row muted - the work is done, no need for attention
func displayRefGroup(depType types.DependencyType, refs []*types.IssueWithDependencyMetadata) {
	// Get emoji for type
	emoji := getRefTypeEmoji(depType)
	fmt.Printf("\n  %s %s (%d):\n", emoji, depType, len(refs))

	for _, ref := range refs {
		// Closed items: mute entire row since the work is complete
		if ref.Status == types.StatusClosed {
			fmt.Printf("    %s: %s %s\n",
				ui.RenderMuted(ref.ID),
				ui.RenderMuted(displayTitle(ref.Title)),
				ui.RenderMuted(fmt.Sprintf("[P%d - %s]", ref.Priority, ref.Status)))
			continue
		}

		// Active items: color ID based on status
		var idStr string
		switch ref.Status {
		case types.StatusOpen:
			idStr = ui.StatusOpenStyle.Render(ref.ID)
		case types.StatusInProgress:
			idStr = ui.StatusInProgressStyle.Render(ref.ID)
		case types.StatusBlocked:
			idStr = ui.StatusBlockedStyle.Render(ref.ID)
		default:
			idStr = ref.ID
		}
		fmt.Printf("    %s: %s [P%d - %s]\n", idStr, displayTitle(ref.Title), ref.Priority, ref.Status)
	}
}

// getRefTypeEmoji returns an emoji for a dependency/reference type
func getRefTypeEmoji(depType types.DependencyType) string {
	switch depType {
	case types.DepUntil:
		return "⏳" // Hourglass - waiting until
	case types.DepCausedBy:
		return "⚡" // Lightning - triggered by
	case types.DepValidates:
		return "✅" // Checkmark - validates
	case types.DepBlocks:
		return "🚫" // Blocked
	case types.DepParentChild:
		return "↳" // Child arrow
	case types.DepRelatesTo, types.DepRelated:
		return "↔" // Bidirectional
	case types.DepTracks:
		return "👁" // Watching
	case types.DepDiscoveredFrom:
		return "◊" // Diamond - discovered
	case types.DepSupersedes:
		return "⬆" // Upgrade
	case types.DepDuplicates:
		return "🔄" // Duplicate
	case types.DepRepliesTo:
		return "💬" // Chat
	case types.DepApprovedBy:
		return "👍" // Approved
	case types.DepAuthoredBy:
		return "✏" // Authored
	case types.DepAssignedTo:
		return "👤" // Assigned
	default:
		return "→" // Default arrow
	}
}
