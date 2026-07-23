package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// fetchDependencyCountsForDuplicates returns per-issue dependency counts for the
// bd duplicates structural-scoring pass, proxied-aware (beads-igmz). In
// proxiedServerMode the global `store` is nil, so route through the UOW
// DependencyUseCase().CountsByIssueIDs (the same usecase the search proxied
// handler uses); otherwise use the direct store.
func fetchDependencyCountsForDuplicates(ctx context.Context, issueIDs []string) (map[string]*types.DependencyCounts, error) {
	if usesProxiedServer() {
		uw, err := openProxiedListUOW(ctx)
		if err != nil {
			return nil, err
		}
		defer uw.Close(ctx)
		return uw.DependencyUseCase().CountsByIssueIDs(ctx, issueIDs)
	}
	return store.GetDependencyCounts(ctx, issueIDs)
}

// fetchDuplicatesIssuesProxied fetches the full issue set for bd duplicates via
// the proxied unit-of-work stack, for hub-connected crew where the global
// `store` is nil (beads-igmz). It mirrors the direct path's read fetch
// (duplicates.go: store.SearchIssues with an empty filter) through the UOW
// IssueUseCase — the same usecase the landed search/list/find-duplicates
// proxied handlers use. The client-side grouping/render is store-free and is
// shared unchanged. The --auto-merge write path is now proxied too via
// performMergeProxied (beads-ox2id).
func fetchDuplicatesIssuesProxied(ctx context.Context) ([]*types.Issue, error) {
	uw, err := openProxiedListUOW(ctx)
	if err != nil {
		return nil, err
	}
	defer uw.Close(ctx)

	page, err := uw.IssueUseCase().SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// performMergeProxied is the proxied-server twin of performMerge (duplicates.go)
// for `bd duplicates --auto-merge` on hub-connected crew, where the global
// `store` is nil (beads-ox2id). The direct path used store.GetDependentsWithMetadata
// + transact(), neither of which has a UOW equivalent, so the caller previously
// REJECTED --auto-merge under proxiedServerMode (fail-loud, safe but unusable).
// The incoming-dependents read now exists on the UOW (ListWithIssueMetadata +
// DepDirectionIn → GetDependentsWithMetadataInTx, which scans BOTH dependencies
// and wisp_dependencies) — the same read beads-q8hxe used for the proxied
// duplicate/supersede incoming-edge migration — so the "no UOW usecase" premise
// in the old duplicates.go:58 comment is stale.
//
// It preserves every invariant the direct performMerge holds:
//   - PER-SOURCE atomicity (njnw / beads-zcq86): reparent-children +
//     transfer-blocking-edges + close-source + duplicates-link all commit on ONE
//     UOW per source, or nothing changes for that source (a mid-sequence failure
//     rolls back via the deferred uw.Close). A conflicting source is recorded in
//     result["errors"] and skipped; other sources still merge.
//   - beads-706mw: transfer BOTH parent-child AND blocking (blocks /
//     conditional-blocks / waits-for) inbound edges from source→target, so a
//     dependent blocked-by the loser stays blocked-by the canonical instead of
//     silently unblocking, and children reparent instead of orphaning. Routed by
//     the DEPENDENT's kind (a wisp dependent's edge lives in wisp_dependencies)
//     via proxiedResolveIssueOrWisp (pega7).
//   - waits-for gate metadata is preserved by re-reading the dependent's outbound
//     records (GetIssueDependencyRecords/GetWispDependencyRecords) before the
//     remove+re-add — GetDependentsWithMetadataInTx drops Dependency.Metadata.
//   - a self-edge (target itself is a dependent of the loser) is dropped, not
//     re-pointed to target→target (AddDependency rejects self-deps).
//   - beads-chf1w: the merged-away source links to the target with a
//     types.DepDuplicates edge (not "related"), so the beads-8nugc reopen guard
//     reasons about it.
//   - beads-z252q: run the completed-molecule auto-close cascade
//     (autoCloseProxiedCompletedMolecule) pre-commit so merging a molecule's
//     FINAL step auto-closes the completed root in the SAME commit.
//   - beads-r3m8v: GC-survivable audit-file trail emitted AFTER commit.
//
// Unlike the direct path it does NOT preserve the "Duplicate of X" close reason
// as a stored close_reason on the wisp/issue when the source resolves as a wisp
// (CloseWisp/CloseIssue take a Reason param, which we set) — parity with the
// direct tx.CloseIssue(reason). Returns the same result map shape performMerge
// returns so the caller's JSON/text rendering and collectMergeErrors are shared
// unchanged.
func performMergeProxied(ctx context.Context, targetID string, sourceIDs []string) map[string]interface{} {
	result := map[string]interface{}{
		"target":     targetID,
		"sources":    sourceIDs,
		"closed":     []string{},
		"linked":     []string{},
		"reparented": []string{},
		"errors":     []string{},
	}

	closedIDs := []string{}
	linkedIDs := []string{}
	reparentedIDs := []string{}
	errs := []string{}
	actor := getActor()

	for _, sourceID := range sourceIDs {
		reparented, srcOldStatus, autoClosedRoot, srcBefore, mergeErr := performMergeOneSourceProxied(ctx, targetID, sourceID, actor)
		if mergeErr != nil {
			errs = append(errs, mergeErr.Error())
			continue
		}

		reparentedIDs = append(reparentedIDs, reparented...)
		closedIDs = append(closedIDs, sourceID)
		linkedIDs = append(linkedIDs, sourceID)

		// beads-r3m8v: GC-survivable audit-file trail for the close, at parity
		// with performMerge (duplicates.go) and bd close — emitted AFTER the
		// commit so a rolled-back source records no phantom audit entry.
		auditStatusChange(sourceID, srcOldStatus, "closed", actor, fmt.Sprintf("Duplicate of %s", targetID))
		if autoClosedRoot != "" {
			// beads-jcrp4/zt47w parity: audit the molecule root the cascade closed.
			auditStatusChange(autoClosedRoot, "open", "closed", actor, "all steps complete")
		}

		// beads-usumn: fire the source's mutation hooks (on_update always +
		// on_close on the open→closed transition) at parity with the direct legs.
		// The after-image is a fresh post-commit read of the committed closed
		// state; a hook error is non-fatal (the merge already committed).
		if srcBefore != nil {
			if after := proxiedResolveForNoOp(ctx, sourceID); after != nil {
				if err := fireProxiedUpdateHooks(ctx, srcBefore, after); err != nil {
					result["hook_warnings"] = appendStr(result["hook_warnings"], fmt.Sprintf("%s: %v", sourceID, err))
				}
			}
		}
	}

	result["closed"] = closedIDs
	result["linked"] = linkedIDs
	result["reparented"] = reparentedIDs
	result["errors"] = errs
	return result
}

// appendStr appends s to an interface-held []string (used for optional result
// warning slices), initializing the slice if absent.
func appendStr(existing interface{}, s string) []string {
	if list, ok := existing.([]string); ok {
		return append(list, s)
	}
	return []string{s}
}

// performMergeOneSourceProxied merges a single duplicate source into targetID on
// ONE proxied UOW (per-source njnw atomicity). Returns the reparented dependent
// IDs, the source's pre-close status, any molecule root auto-closed by the
// cascade, the source's pre-close before-image (for hook firing), and an error
// (whole unit rolled back) — the caller records the error and moves on.
func performMergeOneSourceProxied(ctx context.Context, targetID, sourceID, actor string) (reparented []string, oldStatus, autoClosedRoot string, before *types.Issue, err error) {
	uw, uerr := uowProvider.NewUOW(ctx)
	if uerr != nil {
		return nil, "", "", nil, fmt.Errorf("open unit of work for %s: %w", sourceID, uerr)
	}
	// On any early return before Commit, Close rolls the whole per-source unit
	// back — preserving atomicity (no half-reparent, no closed-without-link).
	committed := false
	defer func() {
		if !committed {
			uw.Close(ctx)
		}
	}()

	srcIssue, srcIsWisp := proxiedResolveIssueOrWisp(ctx, uw, sourceID)
	if srcIssue == nil {
		return nil, "", "", nil, fmt.Errorf("failed to resolve %s: not found", sourceID)
	}
	before = srcIssue
	oldStatus = string(srcIssue.Status)
	if oldStatus == "" {
		oldStatus = "open"
	}

	// Read the source's INCOMING dependents (DepDirectionIn scans both
	// dependencies and wisp_dependencies). Route the source-keyed read by the
	// source's own kind so a wisp source's incoming edges are seen.
	var incoming []*types.IssueWithDependencyMetadata
	var incErr error
	if srcIsWisp {
		incoming, incErr = uw.DependencyUseCase().ListWispWithIssueMetadata(ctx, sourceID, domain.DepListFilter{Direction: domain.DepDirectionIn})
	} else {
		incoming, incErr = uw.DependencyUseCase().ListWithIssueMetadata(ctx, sourceID, domain.DepListFilter{Direction: domain.DepDirectionIn})
	}
	if incErr != nil {
		return nil, "", "", nil, fmt.Errorf("read dependents of %s: %w", sourceID, incErr)
	}

	for _, dep := range incoming {
		// beads-706mw: transfer parent-child AND blocking edges; skip provenance.
		if dep.DependencyType != types.DepParentChild && !dep.DependencyType.IsBlockingEdge() {
			continue
		}
		childID := dep.ID
		_, childIsWisp := proxiedResolveIssueOrWisp(ctx, uw, childID)

		// A self-edge (target is itself a dependent of the loser) would create
		// target→target; just drop the stale edge to the closing loser.
		if childID == targetID {
			if rerr := removeDepProxied(ctx, uw, childIsWisp, childID, sourceID, actor); rerr != nil {
				return nil, "", "", nil, fmt.Errorf("remove self-referential edge %s→%s: %w", childID, sourceID, rerr)
			}
			continue
		}

		// Preserve the original edge's metadata (waits-for gate config lives in
		// Dependency.Metadata; the metadata-list read drops it, so recover it
		// from the dependent's outbound records).
		edgeMeta := lookupEdgeMetadataProxied(ctx, uw, childIsWisp, childID, sourceID, dep.DependencyType)

		if rerr := removeDepProxied(ctx, uw, childIsWisp, childID, sourceID, actor); rerr != nil {
			return nil, "", "", nil, fmt.Errorf("remove %s link %s→%s: %w", dep.DependencyType, childID, sourceID, rerr)
		}
		migrated := &types.Dependency{
			IssueID:     childID,
			DependsOnID: targetID,
			Type:        dep.DependencyType,
			Metadata:    edgeMeta,
		}
		if aerr := addDepProxied(ctx, uw, childIsWisp, migrated, actor); aerr != nil {
			return nil, "", "", nil, fmt.Errorf("move %s edge %s to %s: %w", dep.DependencyType, childID, targetID, aerr)
		}
		reparented = append(reparented, childID)
	}

	// Close the source (preserving the "Duplicate of X" reason) and link it to
	// the target with a duplicates edge (beads-chf1w). Route both by the SOURCE
	// kind (a wisp source's edge belongs in wisp_dependencies, its close on the
	// wisps table).
	dupEdge := &types.Dependency{IssueID: sourceID, DependsOnID: targetID, Type: types.DepDuplicates}
	reason := fmt.Sprintf("Duplicate of %s", targetID)
	if srcIsWisp {
		if aerr := uw.DependencyUseCase().AddWispDependency(ctx, dupEdge, actor); aerr != nil {
			return nil, "", "", nil, fmt.Errorf("link %s to %s: %w", sourceID, targetID, aerr)
		}
		if _, cerr := uw.IssueUseCase().CloseWisp(ctx, sourceID, domain.CloseIssueParams{Reason: reason}, actor); cerr != nil {
			return nil, "", "", nil, fmt.Errorf("close %s: %w", sourceID, cerr)
		}
	} else {
		if aerr := uw.DependencyUseCase().AddDependency(ctx, dupEdge, actor); aerr != nil {
			return nil, "", "", nil, fmt.Errorf("link %s to %s: %w", sourceID, targetID, aerr)
		}
		if _, cerr := uw.IssueUseCase().CloseIssue(ctx, sourceID, domain.CloseIssueParams{Reason: reason}, actor); cerr != nil {
			return nil, "", "", nil, fmt.Errorf("close %s: %w", sourceID, cerr)
		}
	}

	// beads-z252q: run the completed-molecule auto-close cascade BEFORE commit so
	// merging a molecule's FINAL step auto-closes the completed root in the SAME
	// commit (a post-commit call would stage the root-close on a UOW then rolled
	// back by the deferred Close). session="" (system action, matching the direct
	// legs).
	autoClosedRoot = autoCloseProxiedCompletedMolecule(ctx, uw, sourceID, actor, "", isJSONOutput())

	if cerr := uw.Commit(ctx, fmt.Sprintf("bd: merge %s into %s", sourceID, targetID)); cerr != nil && !isDoltNothingToCommit(cerr) {
		return nil, "", "", nil, fmt.Errorf("commit merge %s into %s: %w", sourceID, targetID, cerr)
	}
	committed = true
	uw.Close(ctx)
	commandDidWrite.Store(true)
	return reparented, oldStatus, autoClosedRoot, before, nil
}

// removeDepProxied removes a dependency edge routed by the dependent (edge-owner)
// kind: a wisp dependent's edge lives in wisp_dependencies.
func removeDepProxied(ctx context.Context, uw uow.UnitOfWork, isWisp bool, issueID, dependsOnID, actor string) error {
	if isWisp {
		return uw.DependencyUseCase().RemoveWispDependency(ctx, issueID, dependsOnID, actor)
	}
	return uw.DependencyUseCase().RemoveDependency(ctx, issueID, dependsOnID, actor)
}

// addDepProxied adds a dependency edge routed by the dependent (edge-owner) kind.
func addDepProxied(ctx context.Context, uw uow.UnitOfWork, isWisp bool, dep *types.Dependency, actor string) error {
	if isWisp {
		return uw.DependencyUseCase().AddWispDependency(ctx, dep, actor)
	}
	return uw.DependencyUseCase().AddDependency(ctx, dep, actor)
}

// lookupEdgeMetadataProxied recovers the Metadata blob of the childID→dependsOnID
// edge of type depType from the dependent's outbound records (the metadata-list
// read drops Dependency.Metadata). Returns "" if not found or on error — a
// best-effort preservation matching performMerge's direct GetDependencyRecords
// recovery.
func lookupEdgeMetadataProxied(ctx context.Context, uw uow.UnitOfWork, isWisp bool, childID, dependsOnID string, depType types.DependencyType) string {
	var recs map[string][]*types.Dependency
	var rerr error
	if isWisp {
		recs, rerr = uw.DependencyUseCase().GetWispDependencyRecords(ctx, []string{childID})
	} else {
		recs, rerr = uw.DependencyUseCase().GetIssueDependencyRecords(ctx, []string{childID})
	}
	if rerr != nil {
		return ""
	}
	for _, r := range recs[childID] {
		if r.DependsOnID == dependsOnID && r.Type == depType {
			return r.Metadata
		}
	}
	return ""
}
