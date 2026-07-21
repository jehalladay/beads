package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-rejl: proxied-server handler for `bd priority`.
//
// `bd priority <id> <n>` is a documented shorthand for `bd update <id>
// --priority <n>`. The direct path resolves+mutates via the global `store`,
// which is NIL in proxiedServerMode (main.go PersistentPreRun returns early,
// before store init, once uowProvider is set) — so it failed "storage is nil"
// for hub-connected crew, unlike `bd update --priority` which routes to a
// proxied handler. Route it to the SAME update proxied core
// (applyUpdateProxiedOne) so the shorthand stays in lockstep with its long form
// under proxied-server mode. Mirrors beads-qwez (assign/tag) and beads-8xb7
// (defer). Priority is already validated (validation.ValidatePriority) before
// this is called; the proxied core audit-logs the priority change.
func runPriorityProxiedServer(ctx context.Context, id string, priority int) error {
	// beads-helt4: proxied twin of the direct no-op-honesty guard. Setting the
	// priority to its current value is an idempotent no-op; the shared proxied
	// update core (applyUpdateProxiedOne) runs ApplyUpdate+Commit unconditionally,
	// bumping updated_at even when nothing changed (the audit leg is already
	// guarded there, but the write is not). Pre-check the current priority here —
	// on a match, report an honest "no change" (rc=0) and skip the write so no
	// commit / updated_at bump occurs, matching the direct path and the sibling
	// verbs' proxied-twin sweep (proxied-twin-lag class). A genuine change falls
	// through to the shared core unchanged.
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("opening unit of work for %s: %v", id, err)
	}
	current, _ := proxiedResolveIssueOrWisp(ctx, uw, id)
	uw.Close(ctx)
	if current != nil && current.Priority == priority {
		SetLastTouchedID(current.ID)
		if jsonOutput {
			// beads-utby: ARRAY shape, matching the change path + bd update.
			return outputJSON([]*types.Issue{current})
		}
		fmt.Printf("%s %s already P%d (no change)\n",
			ui.RenderInfoIcon(), formatFeedbackID(current.ID, current.Title), priority)
		return nil
	}

	in := &updateInput{fields: map[string]any{"priority": priority}}
	issue, ok := applyUpdateProxiedOne(ctx, id, in, false)
	if !ok {
		return &exitError{Code: 1}
	}

	SetLastTouchedID(issue.ID)
	if jsonOutput {
		// beads-utby: ARRAY shape, matching the direct priority path + bd update.
		return outputJSON([]*types.Issue{issue})
	}
	fmt.Printf("%s Set priority of %s to P%d\n", ui.RenderPass("✓"), formatFeedbackID(issue.ID, issue.Title), priority)
	return nil
}
