package main

import (
	"context"
)

// runDiffProxiedServer shows the diff between two Dolt refs via the proxied
// unit-of-work stack, for hub-connected crew where the global `store` is nil
// (beads-mh3e, t3wg/history umbrella). It mirrors the direct path
// (cmd/bd/diff.go): fetch the diff via the UOW IssueUseCase, then hand off to
// the shared renderDiff() which honors --json + the human-readable rendering.
func runDiffProxiedServer(ctx context.Context, fromRef, toRef string) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	entries, err := uw.IssueUseCase().Diff(ctx, fromRef, toRef)
	if err != nil {
		return HandleErrorRespectJSON("failed to get diff: %v", err)
	}

	return renderDiff(entries, fromRef, toRef)
}
