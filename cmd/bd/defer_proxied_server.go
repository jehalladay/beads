package main

import (
	"context"
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-aocj: proxied-server handler for `bd defer`.
//
// The direct path resolves+mutates via the global `store`, which is NIL in
// proxiedServerMode (main.go PersistentPreRun returns early, before store init,
// once uowProvider is set) — so `bd defer` failed "storage is nil" for
// hub-connected crew, unlike `bd update` which routes to a proxied handler.
// Route to the shared update proxied core (applyUpdateProxiedOne), building the
// same field set the direct path writes: status=deferred (except a PAST --until
// date keeps status=open, beads-jy4r9 leg A), optional defer_until, and an
// optional reason appended to notes. Mirrors beads-1zuh (relate/unrelate) and
// beads-qwez (assign/tag).
func runDeferProxiedServer(ctx context.Context, args []string, deferUntil *time.Time, inPast bool, reason string) error {
	deferredCount := 0
	alreadyDeferredCount := 0
	var deferred []*types.Issue

	// Already-deferred no-op guard (beads-fs01), mirrored on the proxied path so
	// it stays symmetric with the direct defer path (defer.go) — the whole point
	// of the aocj proxied routing was to make `bd defer` behave identically for
	// hub-connected crew. Resolving current state needs a read UOW; the write
	// still goes through applyUpdateProxiedOne (its own UOW), matching how
	// reopen_proxied_server.go (beads-b0tw/6fns) reads current then transitions.
	var readUW uow.UnitOfWork
	if uowProvider != nil {
		if uw, err := uowProvider.NewUOW(ctx); err == nil {
			readUW = uw
			defer readUW.Close(ctx)
		}
	}

	for _, id := range args {
		// Genuine re-defer (new --until or --reason) is a real change and must
		// still succeed; only a bare re-defer of an already-deferred issue is the
		// idempotent no-op we short-circuit.
		if readUW != nil && deferUntil == nil && reason == "" {
			if current, _ := proxiedResolveIssueOrWisp(ctx, readUW, id); current != nil &&
				current.Status == types.StatusDeferred {
				alreadyDeferredCount++
				if jsonOutput {
					deferred = append(deferred, current)
				} else {
					fmt.Printf("%s %s was already deferred (no change)\n",
						ui.RenderInfoIcon(), formatFeedbackID(current.ID, current.Title))
				}
				continue
			}
		}

		// beads-jy4r9 leg A: a past --until date keeps status=open (issue stays
		// ready-visible) instead of a self-contradictory deferred-but-ready state,
		// mirroring the direct path and update --defer <past>. A dateless or
		// future defer still transitions to deferred.
		deferredStatus := string(types.StatusDeferred)
		if deferUntil != nil && inPast {
			deferredStatus = string(types.StatusOpen)
		}
		in := &updateInput{fields: map[string]any{
			"status": deferredStatus,
		}}
		if deferUntil != nil {
			in.fields["defer_until"] = *deferUntil
		}
		if reason != "" {
			// Append the reason to notes via the shared append path (current
			// notes + "\n" + reason), matching the direct defer path.
			in.hasAppendNotes = true
			in.appendNotes = reason
			// Also thread the reason into the status-change audit entry so the
			// GC-flatten-survivable trail matches the direct path's
			// auditStatusChange(..., reason) (defer.go). Without this the proxied
			// status audit records "" while direct records the reason — the sole
			// audit-reason parity gap among the status-transition verbs, since
			// reopen/undefer emit their own reasoned auditStatusChange but defer
			// delegates to the generic update core (beads-tw6qj, jffu family).
			in.auditReason = reason
		}

		issue, ok := applyUpdateProxiedOne(ctx, id, in, false)
		if !ok {
			// applyUpdateProxiedOne already printed the per-item error to stderr.
			continue
		}
		deferredCount++

		if jsonOutput {
			deferred = append(deferred, issue)
		} else if deferUntil != nil && inPast {
			fmt.Printf("%s Scheduled %s for %s (past date — stays in bd ready now)\n",
				ui.RenderAccent("*"), issue.ID, deferUntil.Format("2006-01-02 15:04"))
		} else {
			fmt.Printf("%s Deferred %s\n", ui.RenderAccent("*"), issue.ID)
		}
	}

	if jsonOutput && len(deferred) > 0 {
		if err := outputJSON(deferred); err != nil {
			return err
		}
	}
	if deferredCount > 0 {
		commandDidWrite.Store(true)
	}

	// Every requested ID failed → non-zero exit so scripts don't read false
	// success; partial success (deferredCount>0) keeps rc=0 with its JSON array.
	// An all-idempotent-no-op batch (alreadyDeferredCount>0, beads-fs01) also
	// keeps rc=0 — nothing failed. Mirrors the direct defer path (beads-fg6/fs01)
	// and the update/close batch paths.
	if len(args) > 0 && deferredCount == 0 && alreadyDeferredCount == 0 {
		if jsonOutput {
			return HandleErrorRespectJSON("no issues deferred matching the provided IDs")
		}
		return SilentExit()
	}
	return nil
}
