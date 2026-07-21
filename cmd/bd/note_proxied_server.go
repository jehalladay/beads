package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// runNoteProxiedServer appends a note to an issue (or wisp) via the proxied
// unit-of-work stack, for hub-connected crew where the global `store` is nil
// (beads-45xb, fszd/aocj umbrella). It mirrors the direct path (cmd/bd/note.go)
// and `bd update --append-notes` (update_proxied_server.go buildUpdateSpecForIssue
// hasAppendNotes): append to the existing Notes with a newline separator, apply
// through ApplyUpdate, commit, honor --json + the last-touched marker. Text
// parsing (stdin/file/args) + the empty-text guard already happened in the
// caller, so noteText is non-empty here.
func runNoteProxiedServer(ctx context.Context, id, noteText string) {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		FatalErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	current, err := issueUC.GetIssue(ctx, id)
	if err != nil || current == nil {
		wispCurrent, wispErr := issueUC.GetWisp(ctx, id)
		if wispErr == nil && wispCurrent != nil {
			current = wispCurrent
		} else if err != nil {
			FatalErrorRespectJSON("resolving %s: %v", id, err)
		} else {
			FatalErrorRespectJSON("issue %s not found", id)
		}
	}

	if verr := validateIssueUpdatable(id, current); verr != nil {
		FatalErrorRespectJSON("%s", verr)
	}

	// beads-99zdz: carry the append as an atomic server-side op on the spec
	// (ApplyUpdate → issueRepo.AppendNotes, a single CONCAT_WS) instead of the old
	// client-side read (current.Notes) → concat → whole-blob write into
	// Fields["notes"], which lost an update when two proxied `bd note` appends
	// raced on the same snapshot. Mirrors bd update --append-notes (beads-jscve,
	// buildUpdateSpecForIssue) and the direct path (note.go).
	spec := domain.UpdateSpec{AppendNotes: noteText, HasAppendNotes: true}
	updated, err := issueUC.ApplyUpdate(ctx, id, spec, actor)
	if err != nil {
		FatalErrorRespectJSON("updating %s: %v", id, err)
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: note %s", id)); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("failed to commit: %v", err)
	}

	// Record last-touched so `bd show --current` fallback works after a proxied
	// note, matching the direct path (cmd/bd/note.go) and the proxied update
	// handler (beads-gw7s).
	SetLastTouchedID(id)

	title := ""
	var result *types.Issue = updated
	if result == nil {
		result = current
	}
	if result != nil {
		title = result.Title
	}

	if jsonOutput {
		if result != nil {
			// beads-bjyq: ARRAY shape, matching the direct note path + bd update.
			_ = outputJSON([]*types.Issue{result})
		}
		return
	}
	fmt.Printf("%s Note added to %s\n", ui.RenderPass("✓"), formatFeedbackID(id, title))
}
