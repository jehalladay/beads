package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-aocj: proxied-server handlers for the two update-shorthand commands.
//
// `bd assign` and `bd tag` are documented shorthands for `bd update --assignee`
// and `bd update --add-label`. The direct path resolves and mutates via the
// global `store`, which is NIL in proxiedServerMode (main.go PersistentPreRun
// returns early, before store init, once uowProvider is set) — so both failed
// "storage is nil" for hub-connected crew, unlike `bd update` itself which
// routes through a proxied handler. Route them to the SAME update proxied core
// (applyUpdateProxiedOne) so the shorthands stay in lockstep with their long
// form under proxied-server mode. Mirrors the beads-1zuh relate/unrelate fix.

// runAssignProxiedServer applies `bd assign <id> <assignee>` via the proxied
// update core, matching the direct assign path (assignee normalized, "none"
// folds to unassigned).
func runAssignProxiedServer(ctx context.Context, args []string) error {
	id := args[0]
	assignee := normalizeAssignee(args[1])

	// beads-mpkza: proxied twin of the direct no-op-honesty guard (xqsy). Re-
	// assigning to the current owner (or unassigning an already-unassigned issue)
	// changes nothing, yet the shared proxied update core (applyUpdateProxiedOne)
	// runs ApplyUpdate+Commit unconditionally — bumping updated_at and printing a
	// fake "✓ Assigned/Unassigned" a CI/agent gate reads as a state change. Mirror
	// helt4's runPriorityProxiedServer: pre-check the current assignee here and, on
	// a match, report an honest "no change" (rc=0) and skip the write so no commit /
	// updated_at bump occurs. A genuine change falls through to the shared core.
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("opening unit of work for %s: %v", id, err)
	}
	current, _ := proxiedResolveIssueOrWisp(ctx, uw, id)
	uw.Close(ctx)
	if current != nil && normalizeAssignee(current.Assignee) == assignee {
		SetLastTouchedID(current.ID)
		if jsonOutput {
			// beads-yrtx: ARRAY shape, matching the change path + `bd update`.
			return outputJSON([]*types.Issue{current})
		}
		title := formatFeedbackID(current.ID, current.Title)
		if assignee == "" {
			fmt.Printf("%s already unassigned, no change\n", title)
		} else {
			fmt.Printf("%s already assigned to %s, no change\n", title, assignee)
		}
		return nil
	}

	in := &updateInput{fields: map[string]any{"assignee": assignee}}
	issue, ok := applyUpdateProxiedOne(ctx, id, in, false)
	if !ok {
		return &exitError{Code: 1}
	}

	SetLastTouchedID(issue.ID)
	if jsonOutput {
		// beads-yrtx: ARRAY shape, matching the direct assign path + `bd update`.
		return outputJSON([]*types.Issue{issue})
	}
	if assignee == "" {
		fmt.Printf("%s Unassigned %s\n", ui.RenderPass("✓"), formatFeedbackID(issue.ID, issue.Title))
	} else {
		fmt.Printf("%s Assigned %s to %s\n", ui.RenderPass("✓"), formatFeedbackID(issue.ID, issue.Title), assignee)
	}
	return nil
}

// runTagProxiedServer applies `bd tag <id> <label>` via the proxied update core,
// matching the direct tag path (add a single label).
func runTagProxiedServer(ctx context.Context, args []string) error {
	id := args[0]
	label := args[1]

	// beads-mpkza: proxied twin of the direct label no-op-honesty guard (label.go
	// "unchanged"/qi8t). `bd tag <id> <existing-label>` is a no-op — AddLabelInTx is
	// idempotent so no spurious write occurs, but the proxied handler still printed
	// a fake "✓ Added label" instead of the direct path's honest "label already
	// present ... (no change)". Pre-check the current labels here (loaded via the
	// LabelUseCase, mirroring the direct path's store.GetLabels; labels are not
	// carried on the issue struct) and, on a match, report the honest no-change.
	// A genuinely-new label falls through to the shared core, which also enforces
	// the delimiter/length guards (beads-qxu4) — so only the exact-match no-op is
	// short-circuited here.
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("opening unit of work for %s: %v", id, err)
	}
	current, isWisp := proxiedResolveIssueOrWisp(ctx, uw, id)
	if current != nil {
		var existing []string
		if isWisp {
			existing, _ = uw.LabelUseCase().GetWispLabels(ctx, current.ID)
		} else {
			existing, _ = uw.LabelUseCase().GetLabels(ctx, current.ID)
		}
		want := strings.TrimSpace(label)
		for _, l := range existing {
			if l == want {
				uw.Close(ctx)
				current.Labels = existing
				SetLastTouchedID(current.ID)
				if jsonOutput {
					// beads-yrtx: ARRAY shape, matching the change path + `bd update`.
					return outputJSON([]*types.Issue{current})
				}
				fmt.Printf("%s label '%s' already present on %s (no change)\n",
					ui.RenderInfoIcon(), want, formatFeedbackID(current.ID, current.Title))
				return nil
			}
		}
	}
	uw.Close(ctx)

	in := &updateInput{addLabels: []string{label}}
	issue, ok := applyUpdateProxiedOne(ctx, id, in, false)
	if !ok {
		return &exitError{Code: 1}
	}

	SetLastTouchedID(issue.ID)
	if jsonOutput {
		// beads-yrtx: ARRAY shape, matching the direct tag path + `bd update`.
		return outputJSON([]*types.Issue{issue})
	}
	fmt.Printf("%s Added label %q to %s\n", ui.RenderPass("✓"), label, formatFeedbackID(issue.ID, issue.Title))
	return nil
}
