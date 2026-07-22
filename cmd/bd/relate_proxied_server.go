package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// runRelateProxiedServer is the proxied-server counterpart of runRelate
// (beads-1zuh). It mirrors the direct path's guards — self-relate rejection and
// issue-existence — and creates the bidirectional relates-to edge via the UOW's
// DependencyUseCase, so `bd dep relate` works for hub-connected (proxied) crew
// instead of failing "storage is nil".
func runRelateProxiedServer(ctx context.Context, args []string) error {
	id1, id2 := args[0], args[1]

	if id1 == id2 {
		return HandleErrorRespectJSON("cannot relate an issue to itself")
	}

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	if err := proxiedIssuesExist(ctx, uw, id1, id2); err != nil {
		return err
	}

	// beads-hwgq: mirror the direct path's 57nt honest-no-op report.
	// AddDependencies is idempotent, so re-relating an already-related pair would
	// print "✓ Linked" as if something changed. Report "Already related, no
	// change" (rc=0) instead of a false "Linked".
	//
	// beads-tdylv: gate the no-op on the FULLY-bidirectional check, mirroring
	// the direct path's beads-ri535 fix. ri535 changed runRelate to use
	// relatesToLinkFullyBidirectional (both directions) so `bd relate` on an
	// ASYMMETRIC link (one direction present — from an imported one-sided row or
	// legacy pre-oyy1 data) falls through and HEALS the missing reciprocal
	// instead of falsely reporting "already related, no change". But ri535 only
	// touched relate.go, leaving this proxied twin on the either-direction
	// proxiedRelatesToLinkExists — so every hub-connected (proxied) crew still
	// false-no-op'd an asymmetric link and never healed it. Use the
	// fully-bidirectional check so an asymmetric link falls through to the
	// idempotent AddDependencies below (the present edge is a no-op, the missing
	// reciprocal gets written). The unrelate path keeps the either-direction
	// proxiedRelatesToLinkExists — a half-link should still be removable.
	if fullyRelated, checkErr := proxiedRelatesToLinkFullyBidirectional(ctx, uw, id1, id2); checkErr == nil && fullyRelated {
		if jsonOutput {
			result := map[string]interface{}{"id1": id1, "id2": id2, "related": true, "unchanged": true}
			return outputJSON(result)
		}
		fmt.Printf("%s Already related, no change: %s ↔ %s\n", ui.RenderPass("✓"), id1, id2)
		return nil
	}

	// Bidirectional relates-to, mirroring the direct path (both directions in
	// one UOW so the relation can't end up half-created).
	deps := []*types.Dependency{
		{IssueID: id1, DependsOnID: id2, Type: types.DepRelatesTo},
		{IssueID: id2, DependsOnID: id1, Type: types.DepRelatesTo},
	}
	if _, err := uw.DependencyUseCase().AddDependencies(ctx, deps, actor, domain.BulkAddDepsOpts{}); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: relate %s <-> %s", id1, id2)); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		result := map[string]interface{}{"id1": id1, "id2": id2, "related": true}
		return outputJSON(result)
	}
	fmt.Printf("%s Linked %s ↔ %s\n", ui.RenderPass("✓"), id1, id2)
	return nil
}

// runUnrelateProxiedServer is the proxied-server counterpart of runUnrelate
// (beads-1zuh). It mirrors the direct path incl. the beads-piud no-op guard: a
// no-op unrelate of a never-related pair fails loud instead of a false
// "✓ Unlinked".
func runUnrelateProxiedServer(ctx context.Context, args []string) error {
	id1, id2 := args[0], args[1]

	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	if err := proxiedIssuesExist(ctx, uw, id1, id2); err != nil {
		return err
	}

	// beads-piud parity: reject a no-op removal (no relates-to edge in either
	// direction) so a scripted gate can't read rc=0 as proof the link is gone.
	linkExists, err := proxiedRelatesToLinkExists(ctx, uw, id1, id2)
	if err != nil {
		return HandleErrorRespectJSON("checking relates-to link %s <-> %s: %v", id1, id2, err)
	}
	if !linkExists {
		return HandleErrorRespectJSON("no relates-to link to remove: %s is not related to %s", id1, id2)
	}

	if err := uw.DependencyUseCase().RemoveDependency(ctx, id1, id2, actor); err != nil {
		return HandleErrorRespectJSON("failed to remove relates-to %s -> %s: %v", id1, id2, err)
	}
	if err := uw.DependencyUseCase().RemoveDependency(ctx, id2, id1, actor); err != nil {
		return HandleErrorRespectJSON("failed to remove relates-to %s -> %s: %v", id2, id1, err)
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: unrelate %s <-> %s", id1, id2)); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		result := map[string]interface{}{"id1": id1, "id2": id2, "unrelated": true}
		return outputJSON(result)
	}
	fmt.Printf("%s Unlinked %s ↔ %s\n", ui.RenderPass("✓"), id1, id2)
	return nil
}

// proxiedIssuesExist mirrors the direct path's "issue not found" guard for both
// endpoints via the UOW.
func proxiedIssuesExist(ctx context.Context, uw uow.UnitOfWork, id1, id2 string) error {
	for _, id := range []string{id1, id2} {
		issue, err := uw.IssueUseCase().GetIssue(ctx, id)
		if err != nil {
			return HandleErrorRespectJSON("failed to get issue %s: %v", id, err)
		}
		if issue == nil {
			return HandleErrorRespectJSON("issue not found: %s", id)
		}
	}
	return nil
}

// proxiedRelatesToLinkExists reports whether a relates-to edge exists between
// id1 and id2 in either direction (beads-piud parity, proxied path).
func proxiedRelatesToLinkExists(ctx context.Context, uw uow.UnitOfWork, id1, id2 string) (bool, error) {
	recs, err := uw.DependencyUseCase().GetIssueDependencyRecords(ctx, []string{id1, id2})
	if err != nil {
		return false, err
	}
	for _, rec := range recs[id1] {
		if rec != nil && rec.DependsOnID == id2 && rec.Type == types.DepRelatesTo {
			return true, nil
		}
	}
	for _, rec := range recs[id2] {
		if rec != nil && rec.DependsOnID == id1 && rec.Type == types.DepRelatesTo {
			return true, nil
		}
	}
	return false, nil
}

// proxiedRelatesToLinkFullyBidirectional reports whether BOTH id1->id2 AND
// id2->id1 relates-to edges exist (the proxied counterpart of the direct path's
// relatesToLinkFullyBidirectional, beads-ri535). Distinct from
// proxiedRelatesToLinkExists (either-direction): the relate no-op guard must
// only short-circuit when the link is FULLY bidirectional, so a `bd dep relate`
// on an ASYMMETRIC link (one direction present, from an imported one-sided row
// or legacy pre-oyy1 data) proceeds to the idempotent AddDependencies and HEALS
// the missing reciprocal instead of falsely reporting "already related". The
// unrelate path keeps the either-direction check, since a half-link should
// still be removable.
func proxiedRelatesToLinkFullyBidirectional(ctx context.Context, uw uow.UnitOfWork, id1, id2 string) (bool, error) {
	recs, err := uw.DependencyUseCase().GetIssueDependencyRecords(ctx, []string{id1, id2})
	if err != nil {
		return false, err
	}
	hasEdge := func(from, to string) bool {
		for _, rec := range recs[from] {
			if rec != nil && rec.DependsOnID == to && rec.Type == types.DepRelatesTo {
				return true
			}
		}
		return false
	}
	return hasEdge(id1, id2) && hasEdge(id2, id1), nil
}
