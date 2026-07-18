package main

import (
	"context"
	"fmt"
	"time"

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
// same field set the direct path writes: status=deferred (always, matching the
// direct `bd defer` which defers regardless of the --until time), optional
// defer_until, and an optional reason appended to notes. Mirrors beads-1zuh
// (relate/unrelate) and beads-qwez (assign/tag).
func runDeferProxiedServer(ctx context.Context, args []string, deferUntil *time.Time, reason string) error {
	deferredCount := 0
	var deferred []*types.Issue

	for _, id := range args {
		in := &updateInput{fields: map[string]any{
			"status": string(types.StatusDeferred),
		}}
		if deferUntil != nil {
			in.fields["defer_until"] = *deferUntil
		}
		if reason != "" {
			// Append the reason to notes via the shared append path (current
			// notes + "\n" + reason), matching the direct defer path.
			in.hasAppendNotes = true
			in.appendNotes = reason
		}

		issue, ok := applyUpdateProxiedOne(ctx, id, in)
		if !ok {
			// applyUpdateProxiedOne already printed the per-item error to stderr.
			continue
		}
		deferredCount++

		if jsonOutput {
			deferred = append(deferred, issue)
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
	// Mirrors the direct defer path (beads-fg6) and the update/close batch paths.
	if len(args) > 0 && deferredCount == 0 {
		if jsonOutput {
			return HandleErrorRespectJSON("no issues deferred matching the provided IDs")
		}
		return SilentExit()
	}
	return nil
}
