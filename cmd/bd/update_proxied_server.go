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
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

func runUpdateProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) {
	if len(args) == 0 {
		FatalErrorRespectJSON("no issue ID provided")
	}

	in := gatherUpdateInput(ctx, cmd)
	if isUpdateInputNoop(in) {
		// beads-b0lq: mirror the direct update path — a no-field update is an
		// idempotent no-op SUCCESS, so under --json emit a parseable no-op
		// status object rather than the plain-text line, keeping the JSON
		// stdout contract intact for machine consumers on this rc=0 path.
		if jsonOutput {
			_ = outputJSON(map[string]string{
				"status":  "noop",
				"message": "no updates specified",
			})
			return
		}
		fmt.Println("No updates specified")
		return
	}

	// --force overrides the close-time integrity guards on the proxied path
	// exactly as it does on the direct path (beads-u8zw); read it once here.
	force, _ := cmd.Flags().GetBool("force")

	// beads-1d32: --append-notes is NON-IDEMPOTENT, so pre-resolve every id and
	// bail before any write when a multi-id batch appends notes — otherwise a
	// bad sibling id leaves the good ids appended, exits non-zero, and the retry
	// double-appends. Mirrors the direct update path (update.go) and bd close's
	// resolve-all-first atomicity. Idempotent flags keep the cwl8 best-effort
	// partial-apply; a single-id batch cannot half-apply.
	if in.hasAppendNotes && len(args) > 1 {
		if err := preresolveProxiedUpdateArgs(ctx, args); err != nil {
			FatalErrorRespectJSON("%v", err)
		}
	}

	// beads-nfr6b: a scalar-only no-op update (every set field ∈
	// {status,priority,title,assignee} already equal to the issue's current
	// value, no non-scalar/audit-bearing flag) must NOT write on the proxied
	// path. The shared core (applyUpdateProxiedOne → ApplyUpdate → UpdateIssue)
	// runs unconditionally on len(spec.Fields)>0, so once beads-j91h made the
	// proxied no-op succeed instead of returning ErrNoRows it printed a fake
	// "✓ Updated issue" AND bumped updated_at — the same integrity harm absq1
	// fixed on the direct path (bd stale orders by updated_at ASC and derives
	// daysStale from it, so a self-reported no-op silently reset the staleness
	// clock and hid a stale issue). This mirrors the landed proxied-twin family
	// (helt4/mpkza):
	// the LEAF handler pre-resolves current and reports an honest "no change"
	// (skip the write), leaving the shared core untouched. Only the scalar-only
	// case is guarded; any real change or mixed/non-scalar update falls through
	// to applyUpdateProxiedOne unchanged.
	scalarOnly := onlyScalarUpdateInput(in)

	jsonOut, _ := cmd.Flags().GetBool("json")
	var updated []*types.Issue
	var firstUpdatedID string
	failedCount := 0

	for _, id := range args {
		if scalarOnly {
			if current := proxiedResolveForNoOp(ctx, id); current != nil && scalarUpdateIsNoOp(in.fields, current) {
				if firstUpdatedID == "" {
					firstUpdatedID = current.ID
				}
				if jsonOut {
					updated = append(updated, current)
				} else {
					fmt.Printf("%s %s already matches (no change)\n",
						ui.RenderInfoIcon(), formatFeedbackID(current.ID, current.Title))
				}
				continue
			}
		}

		issue, ok := applyUpdateProxiedOne(ctx, id, in, force)
		if !ok {
			failedCount++
			continue
		}
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

	// beads-cwl8: exit non-zero on ANY failed id, not only when NONE succeeded.
	// The direct path (cmd/bd/update.go) returns SilentExit() when
	// processedCount < len(args) (beads-4i20, matching bd close/delete), so a
	// proxied crew scripting `bd update a b c || fail` must see a partial
	// failure too — previously a partial batch (some ok, some bad) returned
	// rc=0. Successes are still applied + printed/emitted above; this only
	// fixes rc.
	if failedCount > 0 {
		// beads-j43d: on a wholly-failed batch (nothing updated) under --json,
		// emit a stdout JSON error object instead of a bare os.Exit(1) with empty
		// stdout — mirrors the direct path (update.go) so a --json consumer can
		// distinguish "command failed" from "produced no output". A partial batch
		// already emitted the JSON array above and keeps the plain non-zero exit.
		if jsonOut && len(updated) == 0 {
			FatalErrorRespectJSON("no issues updated matching the provided IDs")
		}
		os.Exit(1)
	}
}

// proxiedResolveForNoOp resolves an id to its current issue/wisp for the
// beads-nfr6b scalar-no-op pre-check, using its own read-only UOW (opened and
// closed here, no mutation) exactly as the landed proxied-twin leaf handlers do
// (assign_tag_proxied_server.go / priority_proxied_server.go). Returns nil if
// the id does not resolve — the caller then falls through to
// applyUpdateProxiedOne, which reports the not-found per-item error uniformly.
func proxiedResolveForNoOp(ctx context.Context, id string) *types.Issue {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		// A UOW-open failure here is not fatal to the no-op check — fall through
		// to applyUpdateProxiedOne, which surfaces the error per-item.
		return nil
	}
	defer uw.Close(ctx)
	current, _ := proxiedResolveIssueOrWisp(ctx, uw, id)
	return current
}

// preresolveProxiedUpdateArgs resolves every id in a batch WITHOUT mutating,
// returning the first resolution error so the caller can bail before any write
// (beads-1d32). Used only for the non-idempotent --append-notes path where a
// partial apply would double-append on retry.
func preresolveProxiedUpdateArgs(ctx context.Context, args []string) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return fmt.Errorf("opening unit of work: %v", err)
	}
	defer uw.Close(ctx)
	for _, id := range args {
		current, _ := proxiedResolveIssueOrWisp(ctx, uw, id)
		if current == nil {
			return fmt.Errorf("Issue %s not found", id)
		}
	}
	return nil
}

func applyUpdateProxiedOne(ctx context.Context, id string, in *updateInput, force bool) (*types.Issue, bool) {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		// beads-vuyx: per-item errors honor the fg6 JSON-stderr contract under
		// --json (structured JSON error object to stderr; stdout stays a pure
		// success payload), matching the direct update path (update.go
		// reportItemError) and the proxied show handler. Plain text otherwise.
		reportItemError("Error opening unit of work for %s: %v", id, err)
		return nil, false
	}
	defer uw.Close(ctx)

	issueUC := uw.IssueUseCase()
	current, isWisp := proxiedResolveIssueOrWisp(ctx, uw, id)
	if current == nil {
		reportItemError("Issue %s not found", id)
		return nil, false
	}
	if err := validateIssueUpdatable(id, current); err != nil {
		reportItemError("%s", err)
		return nil, false
	}

	// Close-integrity guards (beads-u8zw): `update --status closed` (and the
	// epic-demote / closed-child-reopen transitions) reach the same terminal
	// states the direct update path guards at cmd/bd/update.go:429-496 (the
	// zgku epic-with-open-children, 2hkd demote, and b0tw child-reopen guards).
	// The proxied handler bypassed all of them — it applied the field update via
	// ApplyUpdate with no guard, so `bd update <epic> --status closed` (open
	// children), `bd update <blocked> --status closed`, and reopening a closed
	// child under a closed epic all silently succeeded via the LIVE proxied path
	// where the direct path refuses. Enforce the same invariants here, all
	// overridable with --force, matching `bd close --force`.
	if !force {
		if err := checkProxiedUpdateCloseGuards(ctx, uw, id, current, isWisp, in.fields, in.reparent); err != nil {
			reportItemError("%s", err)
			return nil, false
		}
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

	// beads-9y2f3: on a genuine open->closed transition, run the completed-molecule
	// auto-close cascade — the PROXIED twin of the direct fix (beads-zzp26). The
	// direct `bd update --status closed` (update.go) and the proxied CLOSE path
	// (close_proxied_server.go) both auto-close a molecule/wisp root when its FINAL
	// step closes; only the proxied UPDATE path dropped it, so closing a molecule's
	// last step via `bd update --status closed` on a hub-connected crew left the
	// root stuck OPEN. Reuses the SAME helper the proxied close path uses, and runs
	// BEFORE uw.Commit so the staged root-close lands in this same commit (a
	// post-commit call would be rolled back by the deferred uw.Close — same
	// ordering constraint as the proxied supersede/duplicate leg, beads-26gea).
	// session="" (system action). Guarded to the real open->closed transition,
	// matching checkProxiedUpdateCloseGuards' condition.
	if updated != nil && updated.Status == types.StatusClosed && current.Status != types.StatusClosed {
		autoCloseProxiedCompletedMolecule(ctx, uw, id, actor, "", isJSONOutput())
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

// checkProxiedUpdateCloseGuards enforces, on the proxied update path, the same
// close-time integrity invariants the direct path enforces at cmd/bd/update.go
// (beads-u8zw). It mirrors the direct-path guards (zgku/2hkd/b0tw) using the
// UOW use-cases, exactly as the proxied close handler does (close_proxied_server.go).
// Caller is responsible for the --force bypass (this runs only when !force).
func checkProxiedUpdateCloseGuards(ctx context.Context, uw uow.UnitOfWork, id string, current *types.Issue, isWisp bool, fields map[string]any, reparent *string) error {
	// zgku: refuse closing an epic with open children on a real open->closed
	// transition (already-closed is a no-op close).
	if newStatus, ok := fields["status"].(string); ok && newStatus == "closed" &&
		current.Status != types.StatusClosed {
		// beads-6b9pz: widen from bare TypeEpic to the shared
		// isAutoClosingParentType predicate (epic OR molecule/wisp root),
		// matching bigro's close.go forward-guard widening. CountOpenChildren/
		// CountOpenWispChildren count parent-child children regardless of type.
		if isAutoClosingParentType(current) {
			var openChildren int
			var err error
			if isWisp {
				openChildren, err = uw.IssueUseCase().CountOpenWispChildren(ctx, id)
			} else {
				openChildren, err = uw.IssueUseCase().CountOpenChildren(ctx, id)
			}
			if err == nil && openChildren > 0 {
				return fmt.Errorf("cannot close %s: %d open child issue(s); close children first or use --force to override", id, openChildren)
			}
		}
		// zgku: refuse closing an issue that is blocked by open issues.
		var blocked bool
		var blockers []string
		var err error
		if isWisp {
			blocked, blockers, err = uw.DependencyUseCase().IsWispBlocked(ctx, id)
		} else {
			blocked, blockers, err = uw.DependencyUseCase().IsBlocked(ctx, id)
		}
		if err != nil {
			return fmt.Errorf("Error checking blockers for %s: %v", id, err)
		}
		if blocked && len(blockers) > 0 {
			return fmt.Errorf("cannot close %s: blocked by open issues %v (use --force to override)", id, blockers)
		}
	}

	// 2hkd: refuse demoting an epic (epic -> non-epic) that still has open
	// children — the demote-then-close bypass of the epic close guard.
	if newTypeRaw, ok := fields["issue_type"].(string); ok &&
		current.IssueType == types.TypeEpic && types.IssueType(newTypeRaw).Normalize() != types.TypeEpic {
		var openChildren int
		var err error
		if isWisp {
			openChildren, err = uw.IssueUseCase().CountOpenWispChildren(ctx, id)
		} else {
			openChildren, err = uw.IssueUseCase().CountOpenChildren(ctx, id)
		}
		if err == nil && openChildren > 0 {
			return fmt.Errorf("cannot demote epic %s to %s: %d open child issue(s); close children first or use --force to override", id, newTypeRaw, openChildren)
		}
	}

	// b0tw: refuse reopening a closed child whose parent epic is itself closed
	// (a real closed->open transition), which would recreate the
	// closed-epic-with-open-child state.
	if newStatus, ok := fields["status"].(string); ok &&
		types.Status(newStatus) == types.StatusOpen && current.Status == types.StatusClosed {
		if closedEpics := proxiedClosedEpicParents(ctx, uw, id, isWisp); len(closedEpics) > 0 {
			return fmt.Errorf("cannot reopen %s: its parent %v is closed; reopen the parent first or use --force to override", id, closedEpics)
		}
	}

	// beads-a8a1b: refuse reparenting an OPEN child under a CLOSED epic on the
	// proxied path (the parent-assignment axis of the closed-epic-with-open-child
	// invariant), mirroring the direct path in update.go. Only a genuine
	// violation: a non-empty new parent that is a closed epic AND this child is
	// open. Reparenting a closed child, or under an open/non-epic parent, is
	// unaffected. --force bypass is handled by the caller (runs only when !force).
	if reparent != nil && *reparent != "" && current != nil && current.Status != types.StatusClosed {
		parent, err := uw.IssueUseCase().GetIssue(ctx, *reparent)
		if err == nil && parent != nil &&
			parent.IssueType == types.TypeEpic && parent.Status == types.StatusClosed {
			return fmt.Errorf("cannot reparent %s under closed epic %s: the epic is closed and %s is open (would create a closed epic with an open child); reopen the epic first or use --force to override", id, *reparent, id)
		}
	}

	return nil
}

// proxiedClosedEpicParents returns the IDs of the issue's parents that are
// themselves closed auto-closing roots (epic/molecule/wisp), mirroring
// cmd/bd/close.go closedEpicParents over the UOW. beads-aw9x8: uses the shared
// isAutoClosingParentType so the proxied reopen/update guards fire for a closed
// molecule/ephemeral root too, in lockstep with the direct path.
func proxiedClosedEpicParents(ctx context.Context, uw uow.UnitOfWork, id string, isWisp bool) []string {
	deps, err := proxiedListDeps(ctx, uw, id, isWisp, domain.DepListFilter{Direction: domain.DepDirectionOut})
	if err != nil {
		return nil
	}
	var parents []string
	for _, dep := range deps {
		if dep.DependencyType == types.DepParentChild &&
			isAutoClosingParentType(&dep.Issue) &&
			dep.Issue.Status == types.StatusClosed {
			parents = append(parents, dep.Issue.ID)
		}
	}
	return parents
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
