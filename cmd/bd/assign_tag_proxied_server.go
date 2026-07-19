package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-aocj: proxied-server handlers for the two update-shorthand commands.
//
// `bd assign` and `bd tag` are documented shorthands for `bd update --assignee`
// and `bd update --add-label`. The direct path resolves and mutates via the
// global `store`, which is NIL in proxiedServerMode (main.go PersistentPreRun
// returns early, before store init, once uowProvider is set) — so both failed
// "storage is nil" for hub-connected crew, unlike `bd update` itself which
// routes through a proxied handler. Route them to the SAME update proxied core
// (applyUpdateProxiedOne) so the shorthands stay in lockstep with their long
// form under proxied-server mode. Mirrors the beads-1zuh relate/unrelate fix.

// runAssignProxiedServer applies `bd assign <id> <assignee>` via the proxied
// update core, matching the direct assign path (assignee normalized, "none"
// folds to unassigned).
func runAssignProxiedServer(ctx context.Context, args []string) error {
	id := args[0]
	assignee := normalizeAssignee(args[1])

	in := &updateInput{fields: map[string]any{"assignee": assignee}}
	issue, ok := applyUpdateProxiedOne(ctx, id, in, false)
	if !ok {
		return &exitError{Code: 1}
	}

	SetLastTouchedID(issue.ID)
	if jsonOutput {
		// beads-yrtx: ARRAY shape, matching the direct assign path + `bd update`.
		return outputJSON([]*types.Issue{issue})
	}
	if assignee == "" {
		fmt.Printf("%s Unassigned %s\n", ui.RenderPass("✓"), formatFeedbackID(issue.ID, issue.Title))
	} else {
		fmt.Printf("%s Assigned %s to %s\n", ui.RenderPass("✓"), formatFeedbackID(issue.ID, issue.Title), assignee)
	}
	return nil
}

// runTagProxiedServer applies `bd tag <id> <label>` via the proxied update core,
// matching the direct tag path (add a single label).
func runTagProxiedServer(ctx context.Context, args []string) error {
	id := args[0]
	label := args[1]

	in := &updateInput{addLabels: []string{label}}
	issue, ok := applyUpdateProxiedOne(ctx, id, in, false)
	if !ok {
		return &exitError{Code: 1}
	}

	SetLastTouchedID(issue.ID)
	if jsonOutput {
		// beads-yrtx: ARRAY shape, matching the direct tag path + `bd update`.
		return outputJSON([]*types.Issue{issue})
	}
	fmt.Printf("%s Added label %q to %s\n", ui.RenderPass("✓"), label, formatFeedbackID(issue.ID, issue.Title))
	return nil
}
