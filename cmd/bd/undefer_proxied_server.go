package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// runUndeferProxiedServer is the proxied-server counterpart of the undefer RunE
// (beads-fszd / beads-1zuh routing class). Without it, undefer used the direct
// global store — nil under proxiedServerMode — and returned "database not
// initialized" for hub-connected crew instead of undeferring. It mirrors the
// direct path: per-ID batch, the deferred-status guard, the partial-exit
// contract (rc=1 only when every requested ID failed), and the audit trail.
func runUndeferProxiedServer(ctx context.Context, args []string) error {
	if uowProvider == nil {
		return HandleErrorRespectJSON("proxied-server UOW provider not initialized")
	}

	undeferredIssues := []*types.Issue{}
	undeferredCount := 0

	for _, id := range args {
		issue, fullID, ok := undeferProxiedOne(ctx, id)
		if !ok {
			continue
		}
		undeferredCount++
		auditStatusChange(fullID, string(types.StatusDeferred), string(types.StatusOpen), actor, "")
		if jsonOutput {
			undeferredIssues = append(undeferredIssues, issue)
		} else {
			fmt.Printf("%s Undeferred %s (now open)\n", ui.RenderPass("*"), fullID)
		}
	}

	if jsonOutput && len(undeferredIssues) > 0 {
		if err := outputJSON(undeferredIssues); err != nil {
			return err
		}
	}

	if undeferredCount > 0 {
		commandDidWrite.Store(true)
	}

	// Every requested ID failed (per-item errors already printed to stderr):
	// exit non-zero so scripts don't read false success. Under --json emit a
	// stdout JSON error object to keep the failure parseable (mirrors the direct
	// path + beads-7pcm). Partial success keeps rc=0 and its JSON array above.
	if len(args) > 0 && undeferredCount == 0 {
		if jsonOutput {
			return HandleErrorRespectJSON("no issues undeferred matching the provided IDs")
		}
		return SilentExit()
	}
	return nil
}

// undeferProxiedOne undefers a single issue via the UOW, returning the updated
// issue + resolved id on success. Per-item failures are printed to stderr and
// reported via ok=false so the batch continues (matching the direct path).
func undeferProxiedOne(ctx context.Context, id string) (*types.Issue, string, bool) {
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening unit of work for %s: %v\n", id, err)
		return nil, "", false
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	issue, err := issueUC.GetIssue(ctx, id)
	if err != nil || issue == nil {
		fmt.Fprintf(os.Stderr, "Error resolving %s: %v\n", id, err)
		return nil, "", false
	}
	fullID := issue.ID
	if issue.Status != types.StatusDeferred {
		fmt.Fprintf(os.Stderr, "%s is not deferred (status: %s)\n", fullID, string(issue.Status))
		return nil, "", false
	}

	spec := domain.UpdateSpec{Fields: map[string]any{
		"status":      string(types.StatusOpen),
		"defer_until": nil,
	}}
	updated, err := issueUC.ApplyUpdate(ctx, fullID, spec, actor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error undeferring %s: %v\n", fullID, err)
		return nil, "", false
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: undefer %s", fullID)); err != nil && !isDoltNothingToCommit(err) {
		fmt.Fprintf(os.Stderr, "Error committing undefer %s: %v\n", fullID, err)
		return nil, "", false
	}
	return updated, fullID, true
}
