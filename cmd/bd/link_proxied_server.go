package main

import (
	"context"
	"fmt"
	"strings"

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
// This mirrors the DIRECT link path's validation (link.go): dt.IsValid() then
// dt.IsWellKnown(). beads-9v0d landed the IsWellKnown gate on the direct path
// (parity with `bd dep add`/qfka and `bd create --deps`) AFTER beads-8csa
// created this handler, so the earlier "IsValid-only, do not gate" note here
// went stale — leaving the proxied twin ungated meant hub-connected crew could
// silently persist a typo'd non-gating edge (e.g. "blockd") rc=0 while direct
// crew got rejected. beads-tsu3m restores parity by applying the same gate.
func runLinkProxiedServer(ctx context.Context, id1, id2, depType string) error {
	if isChildOf(id1, id2) {
		return HandleErrorRespectJSON("cannot add dependency: %s is already a child of %s. Children inherit dependency on parent completion via hierarchy. Adding an explicit dependency would create a deadlock", id1, id2)
	}

	dt := types.DependencyType(depType)
	if !dt.IsValid() {
		return HandleErrorRespectJSON("invalid dependency type %q: must be non-empty and at most 32 characters", depType)
	}
	// beads-tsu3m: reject unknown types for parity with the DIRECT link path
	// (beads-9v0d), `bd dep add` (qfka / runDepAddProxiedServer), and
	// `bd create --deps` — all gate on IsWellKnown. Without this, a typo'd
	// blocking type was silently stored as a non-gating custom edge and the
	// dependent stayed ready.
	if !dt.IsWellKnown() {
		return HandleErrorRespectJSON("unknown dependency type %q; valid types: %s", depType, createDepsAcceptedTypeList())
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	// beads-nnsso: mirror the direct link path's honest-no-op report (and the
	// proxied `dep add` epuz precheck). A same-type re-add is idempotent, so an
	// unconditional "✓ Added" is a false success on a no-op — `bd link` must
	// report "unchanged" like its documented alias `bd dep add`. Reuse the shared
	// proxied helpers (source-is-wisp routing + edge existence). External refs are
	// not resolvable via the local dep records, so skip the precheck for them,
	// matching the dep add proxied path (best-effort, same-store-only).
	if !strings.HasPrefix(id2, "external:") {
		srcIsWisp := proxiedDepSourceIsWisp(ctx, uw, id1)
		if proxiedDepEdgeExistsSameType(ctx, uw, id1, id2, dt, srcIsWisp) {
			if jsonOutput {
				return outputJSON(map[string]interface{}{
					"status":        "unchanged",
					"issue_id":      id1,
					"depends_on_id": id2,
					"type":          depType,
				})
			}
			fmt.Printf("%s Dependency already present, no change: %s depends on %s (%s)\n",
				ui.RenderPass("✓"),
				formatFeedbackIDParen(id1, proxiedLookupTitle(ctx, uw, id1)),
				formatFeedbackIDParen(id2, proxiedLookupTitle(ctx, uw, id2)),
				depType)
			return nil
		}
	}

	dep := &types.Dependency{IssueID: id1, DependsOnID: id2, Type: dt}
	// beads-l4hil: route the edge write by source kind, parity with the DIRECT
	// link path (link.go -> store.AddDependency auto-detects the wisp table via
	// IsActiveWispInTx) and with proxied `bd dep add` (zdg7x). The domain dep
	// use case is FLAG-routed (pickDepTable(UseWispsTable)) and never
	// auto-detects, so an unconditional AddDependencies (useWisp=false) INSERT
	// validated against the issues table -> "issue <wisp> not found" for a wisp
	// source. Detect the source kind and route AddWispDependencies vs
	// AddDependencies (reused zdg7x helpers).
	srcIsWisp := proxiedDepSourceIsWisp(ctx, uw, id1)
	if err := proxiedAddDepEdges(ctx, uw, []*types.Dependency{dep}, srcIsWisp, false); err != nil {
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
