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
