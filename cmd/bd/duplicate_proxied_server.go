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
