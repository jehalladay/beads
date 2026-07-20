package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-crys: proxied-server handlers for `bd duplicate <id> --of` and
// `bd supersede <id> --with`.
//
// The direct path (runDuplicate/runSupersede) resolves+mutates via the global
// `store`, which is NIL in proxiedServerMode (main.go PersistentPreRun returns
// early, before store init, once uowProvider is set) — and neither verb is a
// noDbCommand — so both nil-panicked ("storage is nil") for hub-connected crew,
// unlike bd close / bd update which route to a proxied UOW handler.
//
// This is a clean-mirror (per the beads-crys re-classification): the bead's
// original "interface-ext design-gated" note was stale — njnw refactored the
// verbs to the atomic store.LinkAndClose, which needs only GetIssue +
// AddDependency + CloseIssue, all present on the UOW. The njnw atomicity (the
// edge is only durable when the close also commits) is preserved by staging
// AddDependency + CloseIssue on ONE UOW and committing once: a mid-sequence
// failure rolls the whole tx back (uw.Close without Commit).
//
// Mirrors beads-mh3e (diff) + defer_proxied_server.go.

// runDuplicateProxiedServer marks duplicateID a duplicate of the canonical and
// closes it, atomically, over a single proxied UOW.
func runDuplicateProxiedServer(ctx context.Context, duplicateArg, canonicalArg string) error {
	return runLinkAndCloseProxied(ctx, linkAndCloseProxiedInput{
		fromArg:    duplicateArg,
		toArg:      canonicalArg,
		toMissing:  "canonical issue not found: %s",
		selfErr:    "cannot mark an issue as duplicate of itself",
		depType:    types.DepDuplicates,
		commitMsg:  "duplicate",
		successFmt: "%s Marked %s as duplicate of %s (closed)\n",
		jsonFrom:   "duplicate",
		jsonTo:     "canonical",
		noChangeFmt:      "%s %s is already a duplicate of %s (no change)\n",
		alreadyLinkedErr: "%s is already a duplicate of %s — remove the existing duplicates link or reopen %s first (a second canonical would leave %s a duplicate of multiple live issues)",
	})
}

// runSupersedeProxiedServer marks oldID superseded by the replacement and
// closes it, atomically, over a single proxied UOW.
func runSupersedeProxiedServer(ctx context.Context, oldArg, newArg string) error {
	return runLinkAndCloseProxied(ctx, linkAndCloseProxiedInput{
		fromArg:    oldArg,
		toArg:      newArg,
		toMissing:  "replacement issue not found: %s",
		selfErr:    "cannot mark an issue as superseded by itself",
		depType:    types.DepSupersedes,
		commitMsg:  "supersede",
		successFmt: "%s Marked %s as superseded by %s (closed)\n",
		jsonFrom:   "superseded",
		jsonTo:     "replacement",
		noChangeFmt:      "%s %s is already superseded by %s (no change)\n",
		alreadyLinkedErr: "%s is already superseded by %s — remove the existing supersedes link or reopen %s first (a second replacement would leave %s with multiple live successors)",
	})
}

type linkAndCloseProxiedInput struct {
	fromArg, toArg string // the issue being closed, and the target it points at
	toMissing      string // error format when the target issue is not found (one %s)
	selfErr        string // error when from==to
	depType        types.DependencyType
	commitMsg      string // dolt commit message
	successFmt     string // plain-text success (icon, fromID, toID)
	jsonFrom       string // JSON key for the closed id ("duplicate"/"superseded")
	jsonTo         string // JSON key for the target id ("canonical"/"replacement")
	// noChangeFmt is the idempotent no-op notice (icon, fromID, toID) for a
	// same-target re-link (beads-pmaud/beads-cjl9y source-already-linked guard).
	noChangeFmt string
	// alreadyLinkedErr is the rejection when fromID already has a live outgoing
	// edge of this type to a DIFFERENT target (fromID, existing target, fromID,
	// fromID) — the multiple-live-{successors,canonicals} guard.
	alreadyLinkedErr string
}

func runLinkAndCloseProxied(ctx context.Context, in linkAndCloseProxiedInput) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	// On any early return before Commit, Close rolls the tx back — this is what
	// preserves the njnw atomicity (no dangling edge without the close).
	defer uw.Close(ctx)

	// Resolve both issues on the UOW. GetIssue accepts a full ID; the proxied
	// resolve helper also falls back to wisp lookup, matching the direct path's
	// ResolvePartialID+GetIssue which resolves either.
	fromIssue, _ := proxiedResolveIssueOrWisp(ctx, uw, in.fromArg)
	if fromIssue == nil {
		return HandleErrorRespectJSON("failed to resolve %s: not found", in.fromArg)
	}
	toIssue, _ := proxiedResolveIssueOrWisp(ctx, uw, in.toArg)
	if toIssue == nil {
		return HandleErrorRespectJSON(in.toMissing, in.toArg)
	}
	fromID, toID := fromIssue.ID, toIssue.ID

	if fromID == toID {
		return HandleErrorRespectJSON("%s", in.selfErr)
	}

	// beads-wqrfi (proxied twin): reject marking a duplicate of a canonical that
	// is ITSELF a closed duplicate — prevents the dup-of-a-dup chain and the
	// mutual duplicates cycle. Gated to DepDuplicates (matches the direct
	// duplicate.go guard; supersede is a distinct case the bead did not cover).
	// Tell: canonical is closed AND has an outgoing "duplicates" edge.
	if in.depType == types.DepDuplicates && toIssue.Status == types.StatusClosed {
		canonicalDeps, derr := uw.DependencyUseCase().ListWithIssueMetadata(ctx, toID, domain.DepListFilter{})
		if derr != nil {
			return HandleErrorRespectJSON("checking canonical %s: %v", toID, derr)
		}
		for _, d := range canonicalDeps {
			if d.DependencyType == types.DepDuplicates {
				return HandleErrorRespectJSON("canonical %s is itself a closed duplicate (of %s) — mark %s as a duplicate of the live canonical instead, not a duplicate-of-a-duplicate", toID, d.ID, fromID)
			}
		}
	}

	// beads-02v2k (proxied twin): reject a supersede MUTUAL cycle (A superseded-by
	// B, then B superseded-by A) — both close naming the other, no live successor,
	// tracer loops forever. Mirrors the direct duplicate.go supersede guard so the
	// hub-connected path is not a bypass (the dfzre lesson: fix the class at BOTH
	// direct + proxied). Narrow reciprocal-edge check only — does NOT touch
	// cycleCheckTypesFor; an acyclic version chain v1→v2→v3 stays legal (v3 has no
	// back-edge to v2). Tell: the replacement (toID) already supersedes fromID.
	if in.depType == types.DepSupersedes {
		toDeps, derr := uw.DependencyUseCase().ListWithIssueMetadata(ctx, toID, domain.DepListFilter{})
		if derr != nil {
			return HandleErrorRespectJSON("checking replacement %s: %v", toID, derr)
		}
		for _, d := range toDeps {
			if d.DependencyType == types.DepSupersedes && d.ID == fromID {
				return HandleErrorRespectJSON("%s is already superseded by %s — marking %s as superseded by %s would create a supersede cycle (neither has a live successor)", toID, fromID, fromID, toID)
			}
		}
	}

	// beads-pmaud (supersede) + beads-cjl9y (duplicate) proxied twin: reject
	// re-linking fromID when it ALREADY has a live outgoing edge of this type to a
	// DIFFERENT target. AddDependency below would otherwise add a SECOND outgoing
	// edge, leaving fromID with multiple live successors/canonicals ("[C D]") —
	// violating the single-canonical-{replacement,duplicate} invariant the
	// reopen/tracer logic assumes. Runs for BOTH DepSupersedes and DepDuplicates
	// (cjl9y widened this from the supersede-only pmaud guard so the duplicate path
	// is not a bypass — dfzre lesson). Same-target → idempotent no-op (rc0, reflect
	// the stored target); different-target → reject. Mirrors the direct
	// duplicate.go/runSupersede guards so the hub-connected path is not a bypass.
	fromDeps, derr := uw.DependencyUseCase().ListWithIssueMetadata(ctx, fromID, domain.DepListFilter{})
	if derr != nil {
		return HandleErrorRespectJSON("checking %s: %v", fromID, derr)
	}
	for _, d := range fromDeps {
		if d.DependencyType != in.depType {
			continue
		}
		if d.ID == toID {
			if isJSONOutput() {
				return outputJSON(map[string]interface{}{
					in.jsonFrom: fromID,
					in.jsonTo:   toID,
					"status":    "closed",
				})
			}
			fmt.Printf(in.noChangeFmt, ui.RenderInfoIcon(), fromID, toID)
			return nil
		}
		return HandleErrorRespectJSON(in.alreadyLinkedErr, fromID, d.ID, fromID, fromID)
	}

	actor := getActor()

	dep := &types.Dependency{
		IssueID:     fromID,
		DependsOnID: toID,
		Type:        in.depType,
	}
	if err := uw.DependencyUseCase().AddDependency(ctx, dep, actor); err != nil {
		return HandleErrorRespectJSON("failed to add %s dependency: %v", in.depType, err)
	}
	if _, err := uw.IssueUseCase().CloseIssue(ctx, fromID, domain.CloseIssueParams{}, actor); err != nil {
		return HandleErrorRespectJSON("failed to close %s: %v", fromID, err)
	}
	// Single commit: edge + close land together or not at all (njnw atomicity).
	if err := uw.Commit(ctx, in.commitMsg); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("commit %s: %v", in.commitMsg, err)
	}

	commandDidWrite.Store(true)

	if isJSONOutput() {
		return outputJSON(map[string]interface{}{
			in.jsonFrom: fromID,
			in.jsonTo:   toID,
			"status":    "closed",
		})
	}
	fmt.Printf(in.successFmt, ui.RenderPass("✓"), fromID, toID)
	return nil
}
