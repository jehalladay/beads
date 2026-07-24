package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/hooks"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

type reopenProxiedOutcome struct {
	id       string
	before   *types.Issue
	after    *types.Issue
	reopened bool
	// alreadyOpen marks an idempotent no-op success (the id was already open):
	// exit stays 0 and, under --json, the current state is reflected into the
	// reopened array (beads-efyts/hxc2) rather than dropped.
	alreadyOpen bool
}

func runReopenProxiedServer(cmd *cobra.Command, ctx context.Context, args []string, reasons []string) {
	if len(args) == 0 {
		FatalErrorRespectJSON("no issue ID provided")
	}
	// beads-fy8xp: reasons is the positional --reason slice (collected +
	// count-guarded by the caller before the usesProxiedServer split, so the
	// count-mismatch rule fires identically on both paths). Per-ID reason is
	// resolved inside the loop via reasonForReopenIndex; the direct path does the
	// same. A single --reason broadcasts; N map one-per-ID.
	jsonOut, _ := cmd.Flags().GetBool("json")
	// --force overrides the closed-epic-parent guard on the proxied path exactly
	// as it does on the direct path (beads-6fns).
	force, _ := cmd.Flags().GetBool("force")

	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		FatalErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	outcomes := make([]reopenProxiedOutcome, 0, len(args))
	reopenedIssues := []*types.Issue{}
	hasError := false

	// beads-efyts: per-item failures (not-found + the closed-epic-parent /
	// supersede / duplicate guard legs) must not interleave bare plaintext onto
	// a `2>&1 --json` stream — they mirror the direct path's reportReopenItemError
	// (cmd/bd/reopen.go, beads-en28/fg6). Under --json, defer them and flush as
	// JSON error objects only when stdout carries a parseable payload (partial
	// success) or on a no-op-only batch; a WHOLLY-failed batch stays clean so
	// j43d's terminal stdout JSON error is the sole error. Non-JSON keeps the
	// immediate stderr line. reportItemError keys off the global jsonOutput,
	// which is bound to the same persistent --json flag as jsonOut.
	var deferredItemErrors []string
	reportReopenItemError := func(format string, a ...interface{}) {
		if jsonOut {
			deferredItemErrors = append(deferredItemErrors, fmt.Sprintf(format, a...))
			return
		}
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}

	for i, id := range args {
		// beads-fy8xp: per-ID reason, normalized so a whitespace-only positional
		// slot collapses to no-reason (reopen's --reason is optional), matching
		// the direct path (reopen.go).
		reason := normalizeReopenReason(reasonForReopenIndex(reasons, i))
		outcome, ok := reopenProxiedOne(ctx, uw, id, reason, force, reportReopenItemError)
		if !ok {
			hasError = true
			continue
		}
		if !outcome.reopened {
			// beads-efyts/hxc2: an already-open reopen is an idempotent no-op
			// SUCCESS (exit stays 0). Under --json, reflect the current state into
			// the reopened array (mirroring reopen.go / close.go's already-in-state
			// path) instead of dropping it — a --json consumer previously got EMPTY
			// stdout on a no-op. Non-JSON keeps the informational stderr line.
			if outcome.alreadyOpen {
				if jsonOut {
					reopenedIssues = append(reopenedIssues, outcome.after)
				} else {
					fmt.Fprintf(os.Stderr, "%s is already open\n", outcome.id)
				}
			}
			continue
		}
		outcomes = append(outcomes, outcome)
		if jsonOut {
			reopenedIssues = append(reopenedIssues, outcome.after)
		} else {
			suffix := ""
			if reason != "" {
				suffix = ": " + reason
			}
			fmt.Printf("%s Reopened %s%s\n", ui.RenderAccent("↻"), outcome.id, suffix)
		}
	}

	if len(outcomes) > 0 {
		msg := reopenProxiedCommitMessage(outcomes)
		if err := uw.Commit(ctx, msg); err != nil && !isDoltNothingToCommit(err) {
			FatalErrorRespectJSON("commit reopen: %v", err)
		}
		for _, o := range outcomes {
			if err := fireProxiedReopenHooks(ctx, o.after); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: %v\n", o.id, err)
			}
		}
	}

	if jsonOut && len(reopenedIssues) > 0 {
		// Partial success (or an already-open no-op reflected above): stdout
		// carries the reopened array, so any deferred per-item failures flush to
		// stderr as JSON objects (beads-efyts, mirroring reopen.go / en28/fg6).
		for _, msg := range deferredItemErrors {
			reportItemError("%s", msg)
		}
		_ = outputJSON(reopenedIssues)
	}
	if hasError {
		// beads-j43d: on a wholly-failed batch (nothing reopened) under --json,
		// emit a stdout JSON error object instead of a bare os.Exit(1) with empty
		// stdout — mirrors the direct path (reopen.go) so a --json consumer can
		// distinguish "command failed" from "produced no output". Partial success
		// (len>0) already emitted the array above and keeps the plain exit. The
		// wholly-failed batch keeps stderr clean (the deferred item errors are
		// subsumed by this terminal stdout object), matching reopen.go.
		if jsonOut && len(reopenedIssues) == 0 {
			// beads-reopen-json: subsume the ACTUAL per-item guard reason(s)
			// (closed-parent / superseded / duplicate) into this terminal
			// object rather than the generic "no issues reopened matching the
			// provided IDs" — otherwise a --json consumer reads "the id didn't
			// exist" and applies the WRONG remediation. Mirrors the direct
			// reopen.go fix; sibling of beads-9c0o/qpcbg (update) + quodm (close).
			if len(deferredItemErrors) > 0 {
				FatalErrorRespectJSON("%s", strings.Join(deferredItemErrors, "; "))
			}
			FatalErrorRespectJSON("no issues reopened matching the provided IDs")
		}
		os.Exit(1)
	}
	// beads-efyts: no-op-only --json batch (e.g. every id was already open, so
	// hasError stayed false and nothing was reopened) — stdout is empty, so flush
	// the deferred status messages to stderr as JSON objects rather than dropping
	// them (mirrors reopen.go's trailing no-op flush). Guard on empty reopenedIssues
	// so the partial/reflected path above (which already flushed) isn't double-flushed.
	if jsonOut && len(reopenedIssues) == 0 {
		for _, msg := range deferredItemErrors {
			reportItemError("%s", msg)
		}
	}
}

func reopenProxiedOne(ctx context.Context, uw uow.UnitOfWork, id, reason string, force bool, reportItemErr func(string, ...interface{})) (reopenProxiedOutcome, bool) {
	current, isWisp := proxiedResolveIssueOrWisp(ctx, uw, id)
	if current == nil {
		reportItemErr("Issue %s not found", id)
		return reopenProxiedOutcome{}, false
	}
	// beads-3ii21: use the canonical full id for downstream exact-ID ops (parent/
	// supersede/dup guards, reopen), so a bare-hash/partial arg works.
	id = current.ID
	// beads-7us7e: a custom done-category status is a terminal/complete outcome
	// (see the direct-path reopen.go guard) — reopen must apply to it exactly as
	// to literal-closed. Widen the terminal test in lockstep with the direct path
	// so a hub-connected crew gets the same behavior. FROZEN excluded (parked !=
	// done); degraded-safe (empty done-set => literal-'closed' only).
	doneStatuses := doneCategoryStatusSetProxied(ctx, uw)
	if current.Status != types.StatusClosed && !doneStatuses[string(current.Status)] {
		// beads-efyts/hxc2: an already-open reopen is an idempotent no-op SUCCESS,
		// distinct from a non-closed-non-open (in_progress/deferred/blocked) status
		// where reopen deliberately does not apply. The already-open case reflects
		// its target state (handled by the caller under --json); any other
		// non-closed status is an advisory per-item message (JSON error object under
		// --json / plain line otherwise), mirroring the direct path (reopen.go).
		if current.Status == types.StatusOpen {
			return reopenProxiedOutcome{id: id, before: current, after: current, reopened: false, alreadyOpen: true}, true
		}
		// Non-closed-non-open (in_progress/deferred/blocked) is an advisory no-op:
		// reopen does not apply, but it is NOT an error (rc stays 0, matching the
		// direct path which reports it without setting hasError). Return ok=true /
		// reopened=false / alreadyOpen=false so the caller skips it cleanly.
		reportItemErr("%s is not closed (status: %s); reopen only applies to closed issues", id, current.Status)
		return reopenProxiedOutcome{id: id, before: current, after: current, reopened: false}, true
	}

	// Closed-epic-parent guard (beads-b0tw), mirrored on the proxied path
	// (beads-6fns): reopening a closed child whose parent epic is itself closed
	// silently recreates the closed-epic-with-open-child inconsistency the
	// close-guard family prevents. The direct reopen path enforces this
	// (cmd/bd/reopen.go); the proxied handler skipped it. Overridable with --force.
	if !force {
		if closedEpics := proxiedClosedEpicParents(ctx, uw, id, isWisp); len(closedEpics) > 0 {
			reportItemErr("cannot reopen %s: its parent %v is closed; reopen the parent first or use --force to override", id, closedEpics)
			return reopenProxiedOutcome{}, false
		}
		// Superseded-issue guard (beads-8sjb3), mirrored on the proxied path
		// (beads-1mfmk) so a hub-connected crew can't bypass it: `bd supersede
		// old --with new` adds a `supersedes` edge (old→new) and closes old.
		// Reopening old leaves that edge, producing the contradictory "open but
		// superseded by new" state — and since `supersedes` is non-blocking, old
		// reappears in `bd ready`. The direct reopen path enforces this
		// (cmd/bd/reopen.go supersededByTargets); the proxied handler mirrored
		// only the closed-epic-parent guard, never the supersede guard (the
		// beads-dfzre cmd-layer-misses-proxied gap the 8nugc duplicates guard
		// exposed). Overridable with --force.
		if supersedes := proxiedSupersededByTargets(ctx, uw, id, isWisp); len(supersedes) > 0 {
			reportItemErr("cannot reopen %s: it is superseded by %v; remove the supersedes link (bd dep remove %s <target> --type supersedes) or use --force to override", id, supersedes, id)
			return reopenProxiedOutcome{}, false
		}
		// Duplicate-issue guard (beads-8nugc), mirrored on the proxied path so a
		// hub-connected crew can't bypass it: reopening an issue that still carries
		// an outgoing `duplicates` edge leaves the contradictory "open but duplicate"
		// state, and since `duplicates` is non-blocking the issue reappears in
		// `bd ready`. The direct reopen path enforces this (cmd/bd/reopen.go
		// duplicatesTargets); this is its proxied twin. Overridable with --force.
		if dups := proxiedDuplicatesTargets(ctx, uw, id, isWisp); len(dups) > 0 {
			reportItemErr("cannot reopen %s: it is a duplicate of %v; remove the duplicates link (bd dep remove %s <target> --type duplicates) or use --force to override", id, dups, id)
			return reopenProxiedOutcome{}, false
		}
	}

	params := domain.ReopenIssueParams{Reason: reason}
	var (
		res domain.ReopenIssueResult
		err error
	)
	if isWisp {
		res, err = uw.IssueUseCase().ReopenWisp(ctx, id, params, actor)
	} else {
		res, err = uw.IssueUseCase().ReopenIssue(ctx, id, params, actor)
	}
	if err != nil {
		reportItemErr("Error reopening %s: %v", id, err)
		return reopenProxiedOutcome{}, false
	}

	oldStatus := string(current.Status)
	if oldStatus == "" {
		oldStatus = "closed"
	}
	auditStatusChange(id, oldStatus, string(types.StatusOpen), actor, reason)
	return reopenProxiedOutcome{id: id, before: current, after: res.Issue, reopened: res.Reopened}, true
}

// proxiedSupersededByTargets returns the IDs of the issues that supersede `id`
// (i.e. id has an outgoing `supersedes` dep, created by `bd supersede <old>
// --with <new>`). Proxied twin of the direct-path supersededByTargets
// (cmd/bd/reopen.go, beads-8sjb3); mirrors proxiedDuplicatesTargets —
// proxiedListDeps returns the OUTGOING deps, each carrying the target Issue +
// DependencyType.
func proxiedSupersededByTargets(ctx context.Context, uw uow.UnitOfWork, id string, isWisp bool) []string {
	deps, err := proxiedListDeps(ctx, uw, id, isWisp, domain.DepListFilter{Direction: domain.DepDirectionOut})
	if err != nil {
		return nil
	}
	var targets []string
	for _, dep := range deps {
		if dep.DependencyType == types.DepSupersedes {
			targets = append(targets, dep.Issue.ID)
		}
	}
	return targets
}

// proxiedDuplicatesTargets returns the IDs of the canonical issues `id` is a
// duplicate of (i.e. id has an outgoing `duplicates` dep). Proxied twin of the
// direct-path duplicatesTargets (cmd/bd/reopen.go, beads-8nugc); mirrors
// proxiedClosedEpicParents — proxiedListDeps returns the OUTGOING deps, each
// carrying the target Issue + DependencyType.
func proxiedDuplicatesTargets(ctx context.Context, uw uow.UnitOfWork, id string, isWisp bool) []string {
	deps, err := proxiedListDeps(ctx, uw, id, isWisp, domain.DepListFilter{Direction: domain.DepDirectionOut})
	if err != nil {
		return nil
	}
	var targets []string
	for _, dep := range deps {
		if dep.DependencyType == types.DepDuplicates {
			targets = append(targets, dep.Issue.ID)
		}
	}
	return targets
}

func reopenProxiedCommitMessage(outcomes []reopenProxiedOutcome) string {
	ids := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		ids = append(ids, o.id)
	}
	return "bd: reopen " + strings.Join(ids, ", ")
}

func fireProxiedReopenHooks(ctx context.Context, after *types.Issue) error {
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
	return nil
}
