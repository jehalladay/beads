package main

import (
	"context"

	"github.com/steveyegge/beads/internal/storage/domain"
)

// runEpicStatusProxiedServer shows epic closure status via the proxied
// unit-of-work stack, for hub-connected crew where the global `store` is nil
// (beads-92ld). It mirrors the direct path (cmd/bd/epic.go): fetch the epic
// statuses via the UOW IssueUseCase, then hand off to the shared
// renderEpicStatus() which applies --eligible-only and honors --json.
func runEpicStatusProxiedServer(ctx context.Context, eligibleOnly bool) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	epics, err := uw.IssueUseCase().GetEpicsEligibleForClosure(ctx)
	if err != nil {
		return HandleErrorRespectJSON("getting epic status: %v", err)
	}
	return renderEpicStatus(epics, eligibleOnly)
}

// runEpicCloseEligibleProxiedServer closes eligible epics via the proxied
// unit-of-work stack (beads-92ld). Both the read (GetEpicsEligibleForClosure)
// and the write (CloseIssue) go through the same UOW so they share one
// transaction context; it mirrors the direct path by delegating to the shared
// renderEpicCloseEligible(), passing a closeFn backed by the UOW's CloseIssue.
func runEpicCloseEligibleProxiedServer(ctx context.Context, dryRun bool) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	epics, err := issueUC.GetEpicsEligibleForClosure(ctx)
	if err != nil {
		return HandleErrorRespectJSON("getting eligible epics: %v", err)
	}
	return renderEpicCloseEligible(epics, dryRun,
		func(id string) error {
			_, cerr := issueUC.CloseIssue(ctx, id, domain.CloseIssueParams{Reason: "All children completed"}, "system")
			return cerr
		},
		func() error {
			// Commit the UOW transaction so the closes persist (the proxied
			// stack does not autocommit). Tolerate a no-op commit the same way
			// the other proxied write handlers do.
			if cerr := uw.Commit(ctx, "bd: close eligible epics"); cerr != nil && !isDoltNothingToCommit(cerr) {
				return cerr
			}
			return nil
		})
}
