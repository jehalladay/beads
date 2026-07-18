package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-8csa: proxied-server handler for `bd link` (shorthand for `bd dep add`).
//
// The direct path calls fromStore.AddDependency with the global `store`, which
// is NIL in proxiedServerMode (main.go PersistentPreRun returns after the
// uowProvider is set, before store init) → 'storage is nil' for hub-connected
// crew. Route it through the UOW instead.
//
// IMPORTANT: this mirrors the DIRECT link path's validation EXACTLY — dt.IsValid()
// only, NOT the IsWellKnown gate that runDepAddProxiedServer applies (beads-qfka).
// Delegating to runDepAddProxiedServer would make proxied `bd link` REJECT unknown
// types the direct path accepts, i.e. trade one direct-vs-proxied asymmetry for
// the opposite one. Whether `bd link` should reject unknown types at all is a
// separate question tracked by beads-9v0d (owned elsewhere); this fix is
// routing-only and must not change link's accept/reject behavior.
func runLinkProxiedServer(ctx context.Context, id1, id2, depType string) error {
	if isChildOf(id1, id2) {
		return HandleErrorRespectJSON("cannot add dependency: %s is already a child of %s. Children inherit dependency on parent completion via hierarchy. Adding an explicit dependency would create a deadlock", id1, id2)
	}

	dt := types.DependencyType(depType)
	if !dt.IsValid() {
		return HandleErrorRespectJSON("invalid dependency type %q: must be non-empty and at most 32 characters", depType)
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	dep := &types.Dependency{IssueID: id1, DependsOnID: id2, Type: dt}
	if _, err := uw.DependencyUseCase().AddDependencies(ctx, []*types.Dependency{dep}, actor, domain.BulkAddDepsOpts{}); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	proxiedWarnCycles(ctx, uw)

	fromTitle := proxiedLookupTitle(ctx, uw, id1)
	toTitle := proxiedLookupTitle(ctx, uw, id2)

	if err := uw.Commit(ctx, fmt.Sprintf("bd: link %s %s", id1, id2)); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	SetLastTouchedID(id1)

	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"status":        "added",
			"issue_id":      id1,
			"depends_on_id": id2,
			"type":          depType,
		})
	}
	fmt.Printf("%s Linked: %s depends on %s (%s)\n",
		ui.RenderPass("✓"), formatFeedbackIDParen(id1, fromTitle), formatFeedbackIDParen(id2, toTitle), depType)
	return nil
}
