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
}

func runReopenProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) {
	if len(args) == 0 {
		FatalErrorRespectJSON("no issue ID provided")
	}
	reason, _ := cmd.Flags().GetString("reason")
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

	for _, id := range args {
		outcome, ok := reopenProxiedOne(ctx, uw, id, reason, force)
		if !ok {
			hasError = true
			continue
		}
		if !outcome.reopened {
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
		_ = outputJSON(reopenedIssues)
	}
	if hasError {
		// beads-j43d: on a wholly-failed batch (nothing reopened) under --json,
		// emit a stdout JSON error object instead of a bare os.Exit(1) with empty
		// stdout — mirrors the direct path (reopen.go) so a --json consumer can
		// distinguish "command failed" from "produced no output". Partial success
		// (len>0) already emitted the array above and keeps the plain exit.
		if jsonOut && len(reopenedIssues) == 0 {
			FatalErrorRespectJSON("no issues reopened matching the provided IDs")
		}
		os.Exit(1)
	}
}

func reopenProxiedOne(ctx context.Context, uw uow.UnitOfWork, id, reason string, force bool) (reopenProxiedOutcome, bool) {
	current, isWisp := proxiedResolveIssueOrWisp(ctx, uw, id)
	if current == nil {
		fmt.Fprintf(os.Stderr, "Issue %s not found\n", id)
		return reopenProxiedOutcome{}, false
	}
	if current.Status != types.StatusClosed {
		fmt.Fprintf(os.Stderr, "%s is already %s\n", id, current.Status)
		return reopenProxiedOutcome{id: id, before: current, after: current, reopened: false}, true
	}

	// Closed-epic-parent guard (beads-b0tw), mirrored on the proxied path
	// (beads-6fns): reopening a closed child whose parent epic is itself closed
	// silently recreates the closed-epic-with-open-child inconsistency the
	// close-guard family prevents. The direct reopen path enforces this
	// (cmd/bd/reopen.go); the proxied handler skipped it. Overridable with --force.
	if !force {
		if closedEpics := proxiedClosedEpicParents(ctx, uw, id, isWisp); len(closedEpics) > 0 {
			fmt.Fprintf(os.Stderr, "cannot reopen %s: its parent epic %v is closed; reopen the epic first or use --force to override\n", id, closedEpics)
			return reopenProxiedOutcome{}, false
		}
		// Duplicate-issue guard (beads-8nugc), mirrored on the proxied path so a
		// hub-connected crew can't bypass it: reopening an issue that still carries
		// an outgoing `duplicates` edge leaves the contradictory "open but duplicate"
		// state, and since `duplicates` is non-blocking the issue reappears in
		// `bd ready`. The direct reopen path enforces this (cmd/bd/reopen.go
		// duplicatesTargets); this is its proxied twin. Overridable with --force.
		if dups := proxiedDuplicatesTargets(ctx, uw, id, isWisp); len(dups) > 0 {
			fmt.Fprintf(os.Stderr, "cannot reopen %s: it is a duplicate of %v; remove the duplicates link (bd dep remove %s <target> --type duplicates) or use --force to override\n", id, dups, id)
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
		fmt.Fprintf(os.Stderr, "Error reopening %s: %v\n", id, err)
		return reopenProxiedOutcome{}, false
	}

	oldStatus := string(current.Status)
	if oldStatus == "" {
		oldStatus = "closed"
	}
	auditStatusChange(id, oldStatus, string(types.StatusOpen), actor, reason)
	return reopenProxiedOutcome{id: id, before: current, after: res.Issue, reopened: res.Reopened}, true
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
