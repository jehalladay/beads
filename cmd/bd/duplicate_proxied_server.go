package main

import (
	"context"
	"fmt"
	"os"

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
		fromArg:          duplicateArg,
		toArg:            canonicalArg,
		toMissing:        "canonical issue not found: %s",
		selfErr:          "cannot mark an issue as duplicate of itself",
		depType:          types.DepDuplicates,
		commitMsg:        "duplicate",
		successFmt:       "%s Marked %s as duplicate of %s (closed)\n",
		jsonFrom:         "duplicate",
		jsonTo:           "canonical",
		noChangeFmt:      "%s %s is already a duplicate of %s (no change)\n",
		alreadyLinkedErr: "%s is already a duplicate of %s — remove the existing duplicates link or reopen %s first (a second canonical would leave %s a duplicate of multiple live issues)",
	})
}

// runSupersedeProxiedServer marks oldID superseded by the replacement and
// closes it, atomically, over a single proxied UOW.
func runSupersedeProxiedServer(ctx context.Context, oldArg, newArg string) error {
	return runLinkAndCloseProxied(ctx, linkAndCloseProxiedInput{
		fromArg:          oldArg,
		toArg:            newArg,
		toMissing:        "replacement issue not found: %s",
		selfErr:          "cannot mark an issue as superseded by itself",
		depType:          types.DepSupersedes,
		commitMsg:        "supersede",
		successFmt:       "%s Marked %s as superseded by %s (closed)\n",
		jsonFrom:         "superseded",
		jsonTo:           "replacement",
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
	// beads-pm1kh: capture the isWisp bool from BOTH resolutions. pega7 fixed the
	// DIRECT store.LinkAndClose to auto-detect wisp-source routing; this proxied
	// twin previously DISCARDED the bool (`fromIssue, _ := ...`) and then routed
	// every source-side operation (guard-list, AddDependency, CloseIssue) to the
	// permanent issues/dependencies tables unconditionally — so a wisp-source
	// duplicate/supersede on a hub-connected crew failed with "issue <wisp> not
	// found" (the edge INSERT SELECTs issue_type FROM issues WHERE id=<wisp>).
	// The domain UOW splits every table-routed op into an issue and a wisp
	// variant, so the CLI handler must pick per resolved kind — mirroring the
	// direct store's isActiveWisp auto-detect.
	fromIssue, fromIsWisp := proxiedResolveIssueOrWisp(ctx, uw, in.fromArg)
	if fromIssue == nil {
		return HandleErrorRespectJSON("failed to resolve %s: not found", in.fromArg)
	}
	toIssue, toIsWisp := proxiedResolveIssueOrWisp(ctx, uw, in.toArg)
	if toIssue == nil {
		return HandleErrorRespectJSON(in.toMissing, in.toArg)
	}
	fromID, toID := fromIssue.ID, toIssue.ID

	// depsFor / addDep / closeFrom route to the wisp-backed tables when the
	// relevant endpoint resolved as a wisp, so the guards actually see the
	// stored edges and the write lands in wisp_dependencies (not the empty
	// issues side). Guard queries key off the endpoint being inspected; the
	// edge write + close key off the SOURCE (fromID) — matching how the direct
	// store routes by dep.IssueID.
	depsForFrom := func(ctx context.Context) ([]*types.IssueWithDependencyMetadata, error) {
		if fromIsWisp {
			return uw.DependencyUseCase().ListWispWithIssueMetadata(ctx, fromID, domain.DepListFilter{})
		}
		return uw.DependencyUseCase().ListWithIssueMetadata(ctx, fromID, domain.DepListFilter{})
	}
	depsForTo := func(ctx context.Context) ([]*types.IssueWithDependencyMetadata, error) {
		if toIsWisp {
			return uw.DependencyUseCase().ListWispWithIssueMetadata(ctx, toID, domain.DepListFilter{})
		}
		return uw.DependencyUseCase().ListWithIssueMetadata(ctx, toID, domain.DepListFilter{})
	}

	if fromID == toID {
		return HandleErrorRespectJSON("%s", in.selfErr)
	}

	// beads-wqrfi (proxied twin): reject marking a duplicate of a canonical that
	// is ITSELF a closed duplicate — prevents the dup-of-a-dup chain and the
	// mutual duplicates cycle. Gated to DepDuplicates (matches the direct
	// duplicate.go guard; supersede is a distinct case the bead did not cover).
	// Tell: canonical is closed AND has an outgoing "duplicates" edge.
	if in.depType == types.DepDuplicates && toIssue.Status == types.StatusClosed {
		canonicalDeps, derr := depsForTo(ctx)
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
		toDeps, derr := depsForTo(ctx)
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
	fromDeps, derr := depsForFrom(ctx)
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

	// beads-q8hxe: migrate the source's INCOMING structural edges to the target
	// BEFORE closing the source, at parity with the direct supersede path
	// (duplicate.go) — this proxied helper serves BOTH verbs (duplicate +
	// supersede), and both close fromID. Previously the proxied path closed the
	// source without re-pointing its dependents: an incoming blocks /
	// conditional-blocks / waits-for edge left pointing at the closed source
	// silently unblocks the dependent (premature-actionable), and an incoming
	// parent-child edge orphans the child on the closed source instead of
	// reparenting to the live target — the exact regression beads-0c9d1 fixed on
	// the DIRECT supersede path, so the hub-connected path was a bypass (dfzre:
	// fix the class at BOTH direct + proxied). Re-point each migratable incoming
	// edge dependent→fromID to dependent→toID inside this SAME UOW so it lands in
	// the single commit (njnw atomicity — a mid-sequence failure rolls the whole
	// tx back via the deferred uw.Close). Provenance / knowledge edges (related /
	// duplicates / supersedes / discovered-from) are deliberately NOT migrated —
	// they legitimately point at the historical source; isMigratableSupersedeEdge
	// (exported in duplicate.go) gates the {blocks, conditional-blocks, waits-for,
	// parent-child} set the ready/blocked + tree engines act on.
	//
	// The incoming read (DepDirectionIn) reaches GetDependentsWithMetadataInTx,
	// which scans BOTH the dependencies and wisp_dependencies tables, so it
	// catches issue- and wisp-backed dependents regardless of fromID's own kind.
	// Each edge's remove + re-add is routed by the DEPENDENT's kind (a wisp
	// dependent's edge lives in wisp_dependencies), mirroring the direct store's
	// per-endpoint isActiveWisp auto-detect (pega7).
	incoming, incErr := uw.DependencyUseCase().ListWithIssueMetadata(ctx, fromID, domain.DepListFilter{Direction: domain.DepDirectionIn})
	if incErr != nil {
		return HandleErrorRespectJSON("failed to read dependents of %s: %v", fromID, incErr)
	}
	for _, d := range incoming {
		if !isMigratableSupersedeEdge(d.DependencyType) {
			continue
		}
		dependentID := d.ID
		// A self-edge to toID would be created if the dependent IS toID; skip so
		// toID never depends on / parents itself.
		if dependentID == toID {
			continue
		}
		_, depIsWisp := proxiedResolveIssueOrWisp(ctx, uw, dependentID)
		// beads-z8c2v: recover the per-edge Metadata blob BEFORE the remove.
		// The waits-for fanout GATE config (gate=any-children + spawner_id,
		// types.WaitsForMeta) lives in Dependency.Metadata, but the incoming
		// metadata-list read (GetDependentsWithMetadataInTx) DROPS it — so
		// reconstructing `migrated` without it silently reverts a non-default
		// gate to all-children on the target for `bd duplicate`/`bd supersede`
		// under proxied-server mode (store==nil, the hub-connected majority).
		// This is the PROXIED twin of the direct duplicate/supersede fixes
		// (beads-3sq6z / beads-atsyz) + the auto-merge path (beads-706mw); the
		// sibling proxied auto-merge already uses lookupEdgeMetadataProxied
		// (duplicates_proxied_server.go) — mirror it here. Best-effort "" on
		// miss matches the direct GetDependencyRecords recovery pattern.
		edgeMeta := lookupEdgeMetadataProxied(ctx, uw, depIsWisp, dependentID, fromID, d.DependencyType)
		migrated := &types.Dependency{
			IssueID:     dependentID,
			DependsOnID: toID,
			Type:        d.DependencyType,
			Metadata:    edgeMeta,
		}
		if depIsWisp {
			if err := uw.DependencyUseCase().RemoveWispDependency(ctx, dependentID, fromID, actor); err != nil {
				return HandleErrorRespectJSON("remove incoming %s %s→%s: %v", d.DependencyType, dependentID, fromID, err)
			}
			if err := uw.DependencyUseCase().AddWispDependency(ctx, migrated, actor); err != nil {
				return HandleErrorRespectJSON("reattach incoming %s %s→%s: %v", d.DependencyType, dependentID, toID, err)
			}
		} else {
			if err := uw.DependencyUseCase().RemoveDependency(ctx, dependentID, fromID, actor); err != nil {
				return HandleErrorRespectJSON("remove incoming %s %s→%s: %v", d.DependencyType, dependentID, fromID, err)
			}
			if err := uw.DependencyUseCase().AddDependency(ctx, migrated, actor); err != nil {
				return HandleErrorRespectJSON("reattach incoming %s %s→%s: %v", d.DependencyType, dependentID, toID, err)
			}
		}
	}

	dep := &types.Dependency{
		IssueID:     fromID,
		DependsOnID: toID,
		Type:        in.depType,
	}
	// Route the edge write + close by the SOURCE kind (beads-pm1kh). A wisp
	// source's edge belongs in wisp_dependencies and its close on the wisps
	// table; AddDependency/CloseIssue (issues side) would SELECT the wisp from
	// the issues table and fail "not found". Mirrors the direct store, which
	// auto-detects via isActiveWisp(dep.IssueID).
	if fromIsWisp {
		if err := uw.DependencyUseCase().AddWispDependency(ctx, dep, actor); err != nil {
			return HandleErrorRespectJSON("failed to add %s dependency: %v", in.depType, err)
		}
		if _, err := uw.IssueUseCase().CloseWisp(ctx, fromID, domain.CloseIssueParams{}, actor); err != nil {
			return HandleErrorRespectJSON("failed to close %s: %v", fromID, err)
		}
	} else {
		if err := uw.DependencyUseCase().AddDependency(ctx, dep, actor); err != nil {
			return HandleErrorRespectJSON("failed to add %s dependency: %v", in.depType, err)
		}
		if _, err := uw.IssueUseCase().CloseIssue(ctx, fromID, domain.CloseIssueParams{}, actor); err != nil {
			return HandleErrorRespectJSON("failed to close %s: %v", fromID, err)
		}
	}

	// beads-26gea: the proxied duplicate/supersede legs close the source via
	// CloseIssue above but, like the direct legs (duplicate.go) and the proxied
	// UPDATE path, bypass the completed-molecule auto-close cascade that `bd
	// close` runs. Run the SAME cascade the proxied close path uses
	// (autoCloseProxiedCompletedMolecule, close_proxied_server.go) so
	// superseding/duplicating a molecule's FINAL step auto-closes the completed
	// root. Called BEFORE uw.Commit (not after) so the root-close it stages
	// lands in the SAME single commit as the edge+source-close — matching the
	// proxied close path, which also runs the cascade pre-commit (a post-commit
	// call would stage the root-close on a UOW that then gets rolled back by the
	// deferred Close, silently dropping it). session="" (system action, matching
	// the direct legs).
	autoClosedRoots := autoCloseProxiedCompletedMolecule(ctx, uw, fromID, actor, "", isJSONOutput())

	// beads-r3m8v: capture the source's pre-close status for the GC-survivable
	// audit-file entry emitted after the commit. fromIssue was loaded before the
	// close, so it holds the real old status (mirrors close_proxied_server.go's
	// current.Status read).
	fromOldStatus := string(fromIssue.Status)
	if fromOldStatus == "" {
		fromOldStatus = "open"
	}

	// Single commit: edge + source close + any molecule root auto-close land
	// together or not at all (njnw atomicity).
	if err := uw.Commit(ctx, in.commitMsg); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("commit %s: %v", in.commitMsg, err)
	}

	commandDidWrite.Store(true)

	// beads-r3m8v: write the GC-survivable audit-FILE trail (.beads/
	// interactions.jsonl) for the source close, at parity with the direct legs
	// and bd close/update (n4sn class). audit.LogFieldChange writes a cwd-based
	// file, NOT a UOW op, so it must run AFTER uw.Commit succeeds — a pre-commit
	// emit would orphan the entry if the deferred uw.Close rolled the tx back
	// (matches the batch c2pr1 fix, which flushes audit only post-commit). The
	// idempotent same-target no-op returned early above, so reaching here always
	// means a real open→closed transition.
	auditStatusChange(fromID, fromOldStatus, "closed", actor, fmt.Sprintf("%s of %s", in.commitMsg, toID))

	// beads-jcrp4: same GC-survivable audit-file trail for a molecule/wisp root
	// the cascade auto-closed (autoCloseProxiedCompletedMolecule above), emitted
	// AFTER uw.Commit at parity with the source-close audit and the direct
	// autoCloseCompletedMolecule (beads-zt47w). The root was open (helper guards
	// Status != closed).
	for _, root := range autoClosedRoots {
		auditStatusChange(root, "open", "closed", actor, "all steps complete")
	}

	// beads-usumn: fire the source's mutation hooks at parity with the DIRECT
	// legs (duplicate.go). The direct path closes via store.LinkAndClose, whose
	// HookFiringStore decorator fires on_update (hook_decorator.go:180) — and the
	// direct legs now also fire on_close on the open->closed transition (usumn).
	// This proxied twin previously fired ZERO hooks, so a hub-connected crew's
	// duplicate/supersede ran no on_update AND no on_close automation. Reuse the
	// shared fireProxiedUpdateHooks helper (the proxied UPDATE path's fire): it
	// runs on_update always + on_close only on before-open/after-closed — exactly
	// the direct behavior. Emitted AFTER uw.Commit; the after-image is a fresh
	// post-commit read reflecting the committed closed state (fromIssue is the
	// pre-close before-image, so the guard sees a real transition). A hook error
	// is non-fatal (the mutation already committed) — warn, matching the update
	// twin. Skipped for the idempotent same-target no-op, which returned early.
	if after := proxiedResolveForNoOp(ctx, fromID); after != nil {
		if err := fireProxiedUpdateHooks(ctx, fromIssue, after); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", fromID, err)
		}
	}

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
