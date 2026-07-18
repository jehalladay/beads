package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/audit"
	"github.com/steveyegge/beads/internal/hooks"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/fs"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

func runUpdateProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) {
	if len(args) == 0 {
		FatalErrorRespectJSON("no issue ID provided")
	}

	in := gatherUpdateInput(ctx, cmd)
	if isUpdateInputNoop(in) {
		fmt.Println("No updates specified")
		return
	}

	jsonOut, _ := cmd.Flags().GetBool("json")
	var updated []*types.Issue
	var anyUpdated bool
	var firstUpdatedID string

	for _, id := range args {
		issue, ok := applyUpdateProxiedOne(ctx, id, in)
		if !ok {
			continue
		}
		anyUpdated = true
		if firstUpdatedID == "" {
			firstUpdatedID = issue.ID
		}
		if jsonOut {
			updated = append(updated, issue)
		} else {
			fmt.Printf("%s Updated issue: %s\n", ui.RenderPass("✓"), formatFeedbackID(issue.ID, issue.Title))
		}
	}

	if jsonOut && len(updated) > 0 {
		_ = outputJSON(updated)
	}

	// Record last-touched so `bd show --current` fallback works after a proxied
	// update, matching the direct update path (update.go) (beads-gw7s).
	if firstUpdatedID != "" {
		SetLastTouchedID(firstUpdatedID)
	}

	if !anyUpdated {
		os.Exit(1)
	}
}

func applyUpdateProxiedOne(ctx context.Context, id string, in *updateInput) (*types.Issue, bool) {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		// beads-vuyx: per-item failures route through reportItemError so that
		// under --json they emit a structured JSON error object on stderr (the
		// beads-fg6 partial-success contract the direct update path honors),
		// not plain text.
		reportItemError("Error opening unit of work for %s: %v", id, err)
		return nil, false
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	current, err := issueUC.GetIssue(ctx, id)
	if err != nil || current == nil {
		wispCurrent, wispErr := issueUC.GetWisp(ctx, id)
		if wispErr == nil && wispCurrent != nil {
			current = wispCurrent
		} else if err != nil {
			reportItemError("Error resolving %s: %v", id, err)
			return nil, false
		} else {
			reportItemError("Issue %s not found", id)
			return nil, false
		}
	}
	if err := validateIssueUpdatable(id, current); err != nil {
		reportItemError("%s", err)
		return nil, false
	}

	spec, err := buildUpdateSpecForIssue(current, in)
	if err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	updated, err := issueUC.ApplyUpdate(ctx, id, spec, actor)
	if err != nil {
		if errors.Is(err, storage.ErrAlreadyClaimed) || errors.Is(err, storage.ErrNotClaimable) {
			reportItemError("Error claiming %s: %v", id, err)
		} else {
			reportItemError("Error updating %s: %v", id, err)
		}
		return nil, false
	}

	// Audit-log key field changes (survives Dolt GC flatten), mirroring the CLI
	// update path (cmd/bd/update.go) and the proxied close/reopen handlers. Only
	// the proxied UPDATE path was missing this, so it alone dropped the
	// audit-file trail its sibling proxied handlers keep (beads-jffu). Emit only
	// when the field actually changed to avoid a spurious no-op trail.
	if updated != nil {
		if string(updated.Status) != string(current.Status) {
			audit.LogFieldChange(id, "status", string(current.Status), string(updated.Status), actor, "")
		}
		if updated.Assignee != current.Assignee {
			audit.LogFieldChange(id, "assignee", current.Assignee, updated.Assignee, actor, "")
		}
		if updated.Priority != current.Priority {
			audit.LogFieldChange(id, "priority", fmt.Sprintf("%d", current.Priority), fmt.Sprintf("%d", updated.Priority), actor, "")
		}
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: update %s", id)); err != nil && !isDoltNothingToCommit(err) {
		reportItemError("Error committing %s: %v", id, err)
		return nil, false
	}

	if err := fireProxiedUpdateHooks(ctx, current, updated); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s: %v\n", id, err)
	}
	return updated, true
}

func fireProxiedUpdateHooks(ctx context.Context, before, after *types.Issue) error {
	if after == nil {
		return nil
	}
	runner, err := proxiedHookRunner(ctx)
	if err != nil {
		return fmt.Errorf("hook runner: %w", err)
	}
	if runner == nil {
		return nil
	}
	if err := runner.RunSync(hooks.EventUpdate, after); err != nil {
		return fmt.Errorf("on_update hook: %w", err)
	}
	if before != nil &&
		before.Status != types.StatusClosed &&
		after.Status == types.StatusClosed {
		if err := runner.RunSync(hooks.EventClose, after); err != nil {
			return fmt.Errorf("on_close hook: %w", err)
		}
	}
	return nil
}

func proxiedHookRunner(ctx context.Context) (*hooks.Runner, error) {
	if hookRunner != nil {
		return hookRunner, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	fsProvider := fs.NewFileSystemProvider(cwd, newBeadsDirTemplates(), newFileSystemAdapters())
	resolution := fsProvider.BeadsDirFSUseCase().ResolveBeadsDir(ctx)
	if resolution.BeadsDir == "" {
		return nil, nil
	}
	return hooks.NewRunner(filepath.Join(resolution.BeadsDir, "hooks")), nil
}

func buildUpdateSpecForIssue(current *types.Issue, in *updateInput) (domain.UpdateSpec, error) {
	fields := make(map[string]any, len(in.fields))
	for k, v := range in.fields {
		fields[k] = v
	}

	if in.clearDeferStatus && current.Status == types.StatusDeferred {
		fields["status"] = string(types.StatusOpen)
	}
	if in.hasAppendNotes {
		combined := current.Notes
		if combined != "" {
			combined += "\n"
		}
		combined += in.appendNotes
		fields["notes"] = combined
	}
	// Metadata edits carry as per-key slots on the spec so ApplyUpdate applies
	// them atomically SERVER-SIDE (JSON_SET) instead of the old client-side
	// whole-blob read-modify-write that clobbered concurrent edits (beads-jibd).
	// The parse (key=value → typed JSON, key validation) stays client-side — it's
	// pure and has no concurrency hazard; only the read-modify-write moves into
	// the tx. Ordering (merge → sets → unsets) is preserved by ApplyUpdate.
	var metaSets map[string]json.RawMessage
	var metaUnsets []string
	if len(in.setMetadata) > 0 || len(in.unsetMetadata) > 0 {
		var err error
		metaSets, metaUnsets, err = parseMetadataEdits(in.setMetadata, in.unsetMetadata)
		if err != nil {
			return domain.UpdateSpec{}, fmt.Errorf("metadata edit failed for %s: %w", current.ID, err)
		}
	}

	spec := domain.UpdateSpec{
		Fields:         fields,
		Claim:          in.claim,
		AddLabels:      in.addLabels,
		RemoveLabels:   in.removeLabels,
		SetLabels:      in.setLabels,
		Reparent:       in.reparent,
		MetadataSets:   metaSets,
		MetadataUnsets: metaUnsets,
		MetadataMerge:  in.mergeMetadataIn,
	}
	return spec, nil
}
