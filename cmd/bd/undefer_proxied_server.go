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
	// beads-36iz0: exit non-zero only on a GENUINE per-item failure, matching
	// the direct path + reopen (beads-hxc2). A not-deferred input is an
	// idempotent advisory no-op (outcome==undeferNoop), not an error.
	var hasError bool

	for _, id := range args {
		issue, fullID, outcome := undeferProxiedOne(ctx, id)
		switch outcome {
		case undeferOK:
			undeferredCount++
			auditStatusChange(fullID, string(types.StatusDeferred), string(types.StatusOpen), actor, "")
			if jsonOutput {
				undeferredIssues = append(undeferredIssues, issue)
			} else {
				fmt.Printf("%s Undeferred %s (now open)\n", ui.RenderPass("*"), fullID)
			}
		case undeferNoop:
			// not-deferred: idempotent no-op, message already emitted to stderr.
		default: // undeferErr
			hasError = true
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

	// beads-36iz0: exit non-zero only on a genuine failure. A batch of only
	// not-deferred no-ops (hasError==false, undeferredCount==0) is an idempotent
	// success → rc0. Under --json a wholly-failed batch has empty stdout, so emit
	// a stdout JSON error object to keep the failure parseable (mirrors the direct
	// path + beads-7pcm). Partial success keeps rc=0 and its JSON array above.
	if hasError && undeferredCount == 0 {
		if jsonOutput {
			return HandleErrorRespectJSON("no issues undeferred matching the provided IDs")
		}
		return SilentExit()
	}
	return nil
}

// undeferProxiedOutcome distinguishes the three per-item results so the batch
// can treat a genuine error (rc1) apart from an idempotent not-deferred no-op
// (rc0), matching the direct path's hasError split (beads-36iz0).
type undeferProxiedOutcome int

const (
	undeferOK undeferProxiedOutcome = iota
	undeferNoop
	undeferErr
)

// undeferProxiedOne undefers a single issue via the UOW, returning the updated
// issue + resolved id and an outcome. Per-item messages are printed to stderr
// and the batch continues (matching the direct path).
func undeferProxiedOne(ctx context.Context, id string) (*types.Issue, string, undeferProxiedOutcome) {
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening unit of work for %s: %v\n", id, err)
		return nil, "", undeferErr
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	issue, err := issueUC.GetIssue(ctx, id)
	if err != nil || issue == nil {
		fmt.Fprintf(os.Stderr, "Error resolving %s: %v\n", id, err)
		return nil, "", undeferErr
	}
	fullID := issue.ID
	if issue.Status != types.StatusDeferred {
		// beads-36iz0: not-deferred is an idempotent advisory no-op, not an
		// error — mirrors reopen's already-open path (beads-hxc2). rc stays 0.
		fmt.Fprintf(os.Stderr, "%s is not deferred (status: %s)\n", fullID, string(issue.Status))
		return nil, "", undeferNoop
	}

	spec := domain.UpdateSpec{Fields: map[string]any{
		"status":      string(types.StatusOpen),
		"defer_until": nil,
	}}
	updated, err := issueUC.ApplyUpdate(ctx, fullID, spec, actor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error undeferring %s: %v\n", fullID, err)
		return nil, "", undeferErr
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: undefer %s", fullID)); err != nil && !isDoltNothingToCommit(err) {
		fmt.Fprintf(os.Stderr, "Error committing undefer %s: %v\n", fullID, err)
		return nil, "", undeferErr
	}
	return updated, fullID, undeferOK
}
