package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// gate_proxied_server.go makes the `bd gate` subcommand family proxied-server-aware
// (beads-qppc, aocj/1zuh class). In proxiedServerMode the global `store` is nil
// (main.go PersistentPreRun sets uowProvider but returns before newDoltStore), so
// every gate subcommand that touched store.* (SearchIssues/GetIssue/CreateIssue/
// AddDependency/UpdateIssue/CloseIssue/Commit) failed "storage is nil" on
// hub-connected crew. Each store call here has a direct UOW use-case equivalent,
// so these are clean-mirror legs (no interface extension). The `gate check`
// command is routed at the helper level (searchGatesProxied + the proxied legs of
// closeGate/updateGateAwaitID) so its shared, pure evaluation loop is not
// duplicated; the CRUD subcommands below are full-handler mirrors.

// openGateProxiedUOW opens a UOW for the proxied gate handlers.
func openGateProxiedUOW(ctx context.Context) (uow.UnitOfWork, error) {
	if uowProvider == nil {
		return nil, fmt.Errorf("proxied-server UOW provider not initialized")
	}
	return uowProvider.NewUOW(ctx)
}

// searchGatesProxied fetches gate issues via the UOW (mirrors store.SearchIssues
// in gateListCmd / gateCheckCmd). Read-only: no commit.
func searchGatesProxied(ctx context.Context, filter types.IssueFilter) ([]*types.Issue, error) {
	uw, err := openGateProxiedUOW(ctx)
	if err != nil {
		return nil, err
	}
	defer uw.Close(ctx)
	page, err := uw.IssueUseCase().SearchIssues(ctx, "", filter)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// runGateListProxied mirrors gateListCmd's RunE via the UOW.
func runGateListProxied(ctx context.Context, filter types.IssueFilter, allFlag bool) error {
	issues, err := searchGatesProxied(ctx, filter)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	if jsonOutput {
		// beads-tamf: normalize nil→[] so an empty result marshals to a JSON []
		// (not null), matching the direct path and the array contract.
		if issues == nil {
			issues = []*types.Issue{}
		}
		return outputJSON(issues)
	}
	displayGates(issues, allFlag)
	return nil
}

// runGateShowProxied mirrors gateShowCmd's RunE via the UOW.
func runGateShowProxied(ctx context.Context, gateID string) error {
	uw, err := openGateProxiedUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)

	issue, err := uw.IssueUseCase().GetIssue(ctx, gateID)
	if err != nil {
		return HandleErrorRespectJSON("gate not found: %s", gateID)
	}
	if issue.IssueType != "gate" {
		return HandleErrorRespectJSON("%s is not a gate issue (type=%s)", gateID, issue.IssueType)
	}

	if jsonOutput {
		return outputJSON(issue)
	}

	statusSym := "○"
	if issue.Status == types.StatusClosed {
		statusSym = "●"
	}
	fmt.Printf("%s %s - %s\n", statusSym, ui.RenderID(issue.ID), ui.SanitizeForTerminal(issue.Title))
	fmt.Printf("  Status: %s\n", issue.Status)
	fmt.Printf("  Await Type: %s\n", issue.AwaitType)
	if issue.AwaitID != "" {
		fmt.Printf("  Await ID: %s\n", issue.AwaitID)
	}
	if issue.Timeout > 0 {
		fmt.Printf("  Timeout: %s\n", issue.Timeout)
	}
	if len(issue.Waiters) > 0 {
		fmt.Printf("  Waiters:\n")
		for _, w := range issue.Waiters {
			fmt.Printf("    - %s\n", w)
		}
	}
	if issue.Description != "" {
		fmt.Printf("  Description: %s\n", ui.SanitizeForTerminal(issue.Description))
	}
	return nil
}

// runGateCreateProxied mirrors gateCreateCmd's RunE via the UOW: resolve the
// blocked target, create the gate issue, add the blocking dependency, commit once.
func runGateCreateProxied(ctx context.Context, blocksID, gateType, reason, awaitID, timeoutStr string) error {
	uw, err := openGateProxiedUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	depUC := uw.DependencyUseCase()

	targetIssue, err := issueUC.GetIssue(ctx, blocksID)
	if err != nil {
		return HandleErrorRespectJSON("issue not found: %s", blocksID)
	}

	var timeout time.Duration
	if timeoutStr != "" {
		parsed, perr := time.ParseDuration(timeoutStr)
		if perr != nil {
			return HandleErrorRespectJSON("invalid timeout: %v", perr)
		}
		timeout = parsed
	}

	// beads-ds9tr (proxied twin): enforce the gate-type invariants before create,
	// matching the direct path — reject an unknown type or a timer without a
	// timeout so gate check can't be handed an unresolvable gate.
	if verr := validateGateCreate(gateType, awaitID, timeoutStr); verr != nil {
		return HandleErrorRespectJSON("%v", verr)
	}

	title := fmt.Sprintf("Gate: %s", gateType)
	if awaitID != "" {
		title = fmt.Sprintf("Gate: %s %s", gateType, awaitID)
	}

	desc := fmt.Sprintf("Ad-hoc gate blocking %s", targetIssue.ID)
	if reason != "" {
		desc = fmt.Sprintf("%s\n\nReason: %s", desc, reason)
	}

	gate := &types.Issue{
		Title:       title,
		Description: desc,
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.IssueType("gate"),
		AwaitType:   gateType,
		AwaitID:     awaitID,
		Timeout:     timeout,
		CreatedBy:   getActorWithGit(),
		Owner:       getOwner(),
	}

	result, err := issueUC.CreateIssue(ctx, domain.CreateIssueParams{Issue: gate}, actor)
	if err != nil {
		return HandleErrorRespectJSON("creating gate: %v", err)
	}
	gate = result.Issue

	dep := &types.Dependency{
		IssueID:     targetIssue.ID,
		DependsOnID: gate.ID,
		Type:        types.DepBlocks,
	}
	if err := depUC.AddDependency(ctx, dep, actor); err != nil {
		return HandleErrorRespectJSON("adding blocking dependency: %v", err)
	}

	commitMsg := fmt.Sprintf("bd: create gate %s blocking %s", gate.ID, targetIssue.ID)
	if err := uw.Commit(ctx, commitMsg); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		return outputJSON(gate)
	}

	printGateCreateSummary(os.Stdout, gate.ID, gateType, targetIssue.ID, targetIssue.Title, reason, timeout)
	return nil
}

// printGateCreateSummary renders the gate-create confirmation. The target
// issue's title is routed through displayTitle (ui.SanitizeForTerminal) because
// a title can originate from an untrusted import (JSONL/markdown/SCM) carrying
// OSC/CSI terminal-control escapes; printing it raw would inject control
// sequences onto the "Blocks:" line. Display-only — the stored title is
// unchanged. Proxied twin of the direct gate.go gate-create sink. 7n9y
// sink-class slice (beads-tberq).
func printGateCreateSummary(w io.Writer, gateID, gateType, targetID, targetTitle, reason string, timeout time.Duration) {
	fmt.Fprintf(w, "%s Created gate %s (type: %s)\n", ui.RenderPass("✓"), ui.RenderID(gateID), gateType)
	fmt.Fprintf(w, "  Blocks: %s (%s)\n", targetID, displayTitle(targetTitle))
	if reason != "" {
		fmt.Fprintf(w, "  Reason: %s\n", reason)
	}
	if timeout > 0 {
		fmt.Fprintf(w, "  Timeout: %s\n", timeout)
	}
	fmt.Fprintf(w, "\nResolve with: bd gate resolve %s\n", gateID)
}

// runGateResolveProxied mirrors gateResolveCmd's RunE via the UOW.
func runGateResolveProxied(ctx context.Context, gateID, reason string) error {
	// beads-u3lt: mirror the direct gate-resolve --json contract on the proxied
	// path — errors through HandleErrorRespectJSON, success as a JSON doc under
	// --json (was bare HandleError + unconditional plaintext).
	uw, err := openGateProxiedUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)

	issue, err := uw.IssueUseCase().GetIssue(ctx, gateID)
	if err != nil {
		return HandleErrorRespectJSON("gate not found: %s", gateID)
	}
	if issue.IssueType != "gate" {
		return HandleErrorRespectJSON("%s is not a gate issue (type=%s)", gateID, issue.IssueType)
	}

	if _, err := uw.IssueUseCase().CloseIssue(ctx, gateID, domain.CloseIssueParams{Reason: reason}, actor); err != nil {
		return HandleErrorRespectJSON("closing gate: %v", err)
	}
	if err := uw.Commit(ctx, fmt.Sprintf("bd: gate resolve %s", gateID)); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"id":       gateID,
			"resolved": true,
			"reason":   reason,
		})
	}

	fmt.Printf("%s Gate resolved: %s\n", ui.RenderPass("✓"), gateID)
	if reason != "" {
		fmt.Printf("  Reason: %s\n", reason)
	}
	return nil
}

// runGateAddWaiterProxied mirrors gateAddWaiterCmd's RunE via the UOW.
func runGateAddWaiterProxied(ctx context.Context, gateID, waiter string) error {
	if uowProvider == nil {
		return HandleErrorRespectJSON("proxied-server UOW provider not initialized")
	}

	// beads-ki3yd: add-waiter is a read-modify-write on the gate's single `waiters`
	// JSON cell (read the slice, append one, write the whole slice back). On the
	// shared Dolt sql-server two concurrent add-waiters open independent tx
	// snapshots, both read the SAME pre-existing slice, and one loses at commit
	// with `Error 1213 (40001) serialization failure` — surfacing a raw,
	// unactionable Dolt error and dropping that waiter's registration. Wrap the
	// whole read-append-write in RunInTx so a 1213 rollback replays the closure on
	// a FRESH snapshot (re-reading the now-updated slice, re-appending), which is
	// the town-standard proxied-write retry (create/init/quick all use it). This
	// is the beads-1i4u/iq8zr shared-server concurrency class; RunInTx suffices
	// here because add-waiter's larger write set DOES raise a commit-time 1213
	// (unlike ready --claim's silent auto-cell-merge, which needs an advisory
	// lock).
	//
	// alreadyRegistered captures the idempotent-success path (waiter already
	// present) so it survives outside the retry closure without being treated as
	// a domain error.
	var alreadyRegistered bool
	err := uow.RunInTxMsg(ctx, uowProvider, func(uw uow.UnitOfWork) (string, error) {
		alreadyRegistered = false
		issue, err := uw.IssueUseCase().GetIssue(ctx, gateID)
		if err != nil {
			return "", fmt.Errorf("gate not found: %s", gateID)
		}
		if issue.IssueType != "gate" {
			return "", fmt.Errorf("%s is not a gate issue (type=%s)", gateID, issue.IssueType)
		}
		for _, w := range issue.Waiters {
			if w == waiter {
				alreadyRegistered = true
				return "", nil // no-op: nothing to commit
			}
		}
		newWaiters := append(issue.Waiters, waiter)
		updates := map[string]interface{}{
			"waiters": newWaiters,
		}
		if err := uw.IssueUseCase().UpdateIssue(ctx, gateID, updates, actor); err != nil {
			return "", fmt.Errorf("updating gate: %w", err)
		}
		return fmt.Sprintf("bd: gate add-waiter %s", gateID), nil
	})
	if err != nil {
		// beads-mbh7: mirror the direct-path add-waiter --json contract
		// (beads-jial) and the sibling proxied handlers (resolve=beads-u3lt,
		// create, show) — errors through HandleErrorRespectJSON. A bare
		// HandleError left stdout EMPTY + plaintext on stderr under
		// `bd gate add-waiter <bad-id> --json` on a hub-connected crew.
		return HandleErrorRespectJSON("%v", err)
	}

	if alreadyRegistered {
		fmt.Printf("Waiter already registered on gate %s\n", gateID)
		return nil
	}

	fmt.Printf("%s Added waiter to gate %s: %s\n", ui.RenderPass("✓"), gateID, waiter)
	return nil
}

// closeGateProxied closes a gate via the UOW (proxied leg of closeGate, used by
// gate check when routing hub crew). Commits its own write.
func closeGateProxied(gateID, reason string) error {
	ctx := rootCtx
	uw, err := openGateProxiedUOW(ctx)
	if err != nil {
		return err
	}
	defer uw.Close(ctx)
	if _, err := uw.IssueUseCase().CloseIssue(ctx, gateID, domain.CloseIssueParams{Reason: reason}, actor); err != nil {
		return err
	}
	return uw.Commit(ctx, fmt.Sprintf("bd: gate check resolved %s", gateID))
}

// updateGateAwaitIDProxied updates a gate's await_id via the UOW (proxied leg of
// updateGateAwaitID). Commits its own write.
func updateGateAwaitIDProxied(gateID, runID string) error {
	ctx := rootCtx
	uw, err := openGateProxiedUOW(ctx)
	if err != nil {
		return err
	}
	defer uw.Close(ctx)
	updates := map[string]interface{}{
		"await_id": runID,
	}
	if err := uw.IssueUseCase().UpdateIssue(ctx, gateID, updates, actor); err != nil {
		return err
	}
	return uw.Commit(ctx, fmt.Sprintf("bd: gate check discovered run %s", gateID))
}
