package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// runSetStateProxiedServer sets operational state via the proxied unit-of-work
// stack, for hub-connected crew where the global `store` is nil (beads-nzb7,
// fszd/aocj umbrella). set-state is a multi-write (mint child ID + create event
// bead + parent-child dep + label swap); the direct path uses store.* which is
// nil in proxiedServerMode → "storage is nil". This is an interface-extension
// leg: GetNextChildID was added to IssueUseCase (the other ops —
// CreateIssue/AddDependency/AddLabel/RemoveLabel — were already on the UOW
// use-cases). Mirrors cmd/bd/state.go, committing once at the end.
func runSetStateProxiedServer(ctx context.Context, issueID, dimension, newValue, reason string) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	labelUC := uw.LabelUseCase()
	depUC := uw.DependencyUseCase()

	// Resolve existence (exact ID; proxied handlers don't do partial resolution).
	if _, gerr := issueUC.GetIssue(ctx, issueID); gerr != nil {
		return HandleErrorRespectJSON("resolving %s: %v", issueID, gerr)
	}
	fullID := issueID

	labels, err := labelUC.GetLabels(ctx, fullID)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	prefix := dimension + ":"
	var oldLabel, oldValue string
	for _, label := range labels {
		if strings.HasPrefix(label, prefix) {
			oldLabel = label
			oldValue = strings.TrimPrefix(label, prefix)
			break
		}
	}

	newLabel := dimension + ":" + newValue

	if oldLabel == newLabel {
		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"issue_id":  fullID,
				"dimension": dimension,
				"value":     newValue,
				"changed":   false,
			})
		}
		fmt.Printf("(no change: %s already set to %s)\n", dimension, newValue)
		return nil
	}

	eventTitle := fmt.Sprintf("State change: %s → %s", dimension, newValue)
	var eventDesc string
	if oldValue != "" {
		eventDesc = fmt.Sprintf("Changed %s from %s to %s", dimension, oldValue, newValue)
	} else {
		eventDesc = fmt.Sprintf("Set %s to %s", dimension, newValue)
	}
	if reason != "" {
		eventDesc += "\n\nReason: " + reason
	}

	childID, err := issueUC.GetNextChildID(ctx, fullID)
	if err != nil {
		return HandleErrorRespectJSON("generating child ID: %v", err)
	}

	event := &types.Issue{
		ID:          childID,
		Title:       eventTitle,
		Description: eventDesc,
		Status:      types.StatusClosed,
		Priority:    4,
		IssueType:   types.TypeEvent,
		CreatedBy:   getActorWithGit(),
	}
	if _, err := issueUC.CreateIssue(ctx, domain.CreateIssueParams{Issue: event, ExplicitID: childID}, actor); err != nil {
		return HandleErrorRespectJSON("creating event: %v", err)
	}

	dep := &types.Dependency{
		IssueID:     childID,
		DependsOnID: fullID,
		Type:        types.DepParentChild,
	}
	if err := depUC.AddDependency(ctx, dep, actor); err != nil {
		WarnError("failed to add parent-child dependency: %v", err)
	}

	if oldLabel != "" {
		if err := labelUC.RemoveLabel(ctx, fullID, oldLabel, actor); err != nil {
			WarnError("failed to remove old label %s: %v", oldLabel, err)
		}
	}
	if err := labelUC.AddLabel(ctx, fullID, newLabel, actor); err != nil {
		return HandleErrorRespectJSON("adding label: %v", err)
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: set-state %s %s=%s", fullID, dimension, newValue)); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	if jsonOutput {
		result := map[string]interface{}{
			"issue_id":  fullID,
			"dimension": dimension,
			"old_value": oldValue,
			"new_value": newValue,
			"event_id":  childID,
			"changed":   true,
		}
		if oldValue == "" {
			result["old_value"] = nil
		}
		return outputJSON(result)
	}

	fmt.Printf("%s Set %s = %s on %s\n", ui.RenderPass("✓"), dimension, newValue, fullID)
	if oldValue != "" {
		fmt.Printf("  Previous: %s\n", oldValue)
	}
	fmt.Printf("  Event: %s\n", childID)
	return nil
}
