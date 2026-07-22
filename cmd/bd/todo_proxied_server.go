package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-9dsym: bd todo add/list/done are registered top-level commands
// (todo.go init → rootCmd.AddCommand(todoCmd)), so they are REACHABLE for every
// hub-connected/proxied crew — where main.go's PersistentPreRunE returns after
// wiring uowProvider but BEFORE store init, leaving the global `store` (and thus
// getStore()) NIL. The direct RunE bodies call getStore() unconditionally, so
// each subcommand hit "storage is nil" on the proxied path. These handlers route
// through the proxied unit-of-work stack, mirroring bd create/list/close
// (create_proxied_server.go / list_proxied_server.go / close_proxied_server.go) —
// the aocj/fszd/eh0z proxied-routing umbrella missed sibling. Each preserves the
// direct path's exact contract (bd todo add == create -t task; bd todo done ==
// bd close with the k96re guards + 58kg8 audit/auto-close and the xi35 partial
// exit code).

// runTodoAddProxiedServer creates the TODO (a task-type issue) via the proxied
// UOW, mirroring bd create/`bd q` (create_proxied_server.go / quick_proxied_server.go)
// and preserving the direct `bd todo add` output contract (--json marshals the
// issue; plain prints "Created <id>: <title>").
func runTodoAddProxiedServer(ctx context.Context, issue *types.Issue) error {
	if uowProvider == nil {
		return HandleErrorRespectJSON("proxied-server UOW provider not initialized")
	}

	params := domain.CreateIssueParams{Issue: issue}
	var result domain.CreateIssueResult
	if err := uow.RunInTxMsg(ctx, uowProvider, func(uw uow.UnitOfWork) (string, error) {
		var e error
		result, e = uw.IssueUseCase().CreateIssue(ctx, params, getActorWithGit())
		if e != nil {
			return "", e
		}
		return fmt.Sprintf("bd: create %s", result.Issue.ID), nil
	}); err != nil {
		// beads-j9ir parity: honor the --json error contract (bd todo add --json
		// marshals the issue on success, so a store error emits a JSON error object
		// on stdout, not plain-text stderr).
		return HandleErrorRespectJSON("failed to create TODO: %v", err)
	}

	commandDidWrite.Store(true)

	// Record last-touched so `bd show --current` fallback works after a proxied
	// add, matching the proxied create path (create_proxied_server.go, beads-gw7s).
	SetLastTouchedID(result.Issue.ID)

	if jsonOutput {
		// beads-s2oy parity: outputJSON for schema_version + BD_JSON_ENVELOPE.
		return outputJSON(result.Issue)
	}
	fmt.Printf("Created %s: %s\n", ui.RenderID(result.Issue.ID), displayTitle(result.Issue.Title))
	return nil
}

// runTodoListProxiedServer lists TODO (task) issues via the proxied UOW
// SearchIssues, then hands the result to the shared renderTodoList so the output
// is byte-identical to the direct path (runTodoListCore). The task-type/open
// filter was built by the caller before the proxied split.
func runTodoListProxiedServer(ctx context.Context, filter types.IssueFilter) error {
	if uowProvider == nil {
		return HandleErrorRespectJSON("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	page, err := uw.IssueUseCase().SearchIssues(ctx, "", filter)
	if err != nil {
		// beads-j9ir parity: honor the --json error contract on the store-error path.
		return HandleErrorRespectJSON("failed to list TODOs: %v", err)
	}
	return renderTodoList(page.Items)
}

// runTodoDoneProxiedServer closes each TODO via the proxied UOW, preserving the
// direct path's behavior (todo.go doneTodoCmd): per item run the three bd-close
// PRE-close guards unless --force (beads-k96re), close, emit the GC-survivable
// audit-file trail, and run the completed-molecule/wisp/template-epic auto-close
// cascade (beads-58kg8). Per-item failures print to stderr and set the partial
// exit code (beads-xi35); the --json shape is {"closed":[...],"reason":...}.
func runTodoDoneProxiedServer(ctx context.Context, args []string, reason string, force bool) error {
	if uowProvider == nil {
		return HandleErrorRespectJSON("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	actorName := getActorWithGit()

	// autoClosedRoots collects molecule/wisp roots the cascade closed so their
	// GC-survivable audit-file entries are emitted AFTER the commit (beads-jcrp4),
	// matching the direct autoCloseCompletedMolecule ordering.
	type autoClosed struct{ rootID string }
	var closedIDs []string
	var autoClosedRoots []autoClosed
	failedCount := 0

	for _, issueID := range args {
		current, isWisp := proxiedResolveIssueOrWisp(ctx, uw, issueID)
		if current == nil {
			// beads-scl2z (proxied twin): route per-id failures through the
			// json-aware reportItemError so `bd todo done $IDS --json 2>&1 | jq`
			// stays a clean JSON stream and names the failed id, matching the
			// direct todo.go path + `bd close`. Clean-stderr per-item contract
			// family (fg6/92tz/en28/n96g/iwy1k).
			reportItemError("Error: issue %s not found", issueID)
			failedCount++
			continue
		}
		// beads-3ii21: use the canonical full id for downstream exact-ID ops
		// (child/blocker guards, close), so a bare-hash/partial arg works.
		issueID = current.ID

		if !force {
			// (1) open-child guard for auto-closing parents (epic/molecule/wisp
			// root) — mirrors the direct path (todo.go) and close_proxied_server.go.
			if isAutoClosingParentType(current) {
				var openChildren int
				var cerr error
				if isWisp {
					openChildren, cerr = uw.IssueUseCase().CountOpenWispChildren(ctx, issueID)
				} else {
					openChildren, cerr = uw.IssueUseCase().CountOpenChildren(ctx, issueID)
				}
				if cerr == nil && openChildren > 0 {
					reportItemError("Error: cannot close %s: %d open child issue(s); close children first or use --force to override", issueID, openChildren)
					failedCount++
					continue
				}
			}
			// (2) gate-satisfaction guard. The error can embed untrusted external
			// SCM data (gh:pr/gh:run), so sanitize for display (7n9y sink class).
			if gerr := checkGateSatisfaction(current); gerr != nil {
				reportItemError("Error: cannot close %s: %s", issueID, ui.SanitizeForTerminal(gerr.Error()))
				failedCount++
				continue
			}
			// (3) open-blocker guard.
			var blocked bool
			var blockers []string
			var berr error
			if isWisp {
				blocked, blockers, berr = uw.DependencyUseCase().IsWispBlocked(ctx, issueID)
			} else {
				blocked, blockers, berr = uw.DependencyUseCase().IsBlocked(ctx, issueID)
			}
			if berr != nil {
				reportItemError("Error: checking blockers for %s: %v", issueID, berr)
				failedCount++
				continue
			}
			if blocked && len(blockers) > 0 {
				reportItemError("Error: cannot close %s: blocked by open issues %v (use --force to override)", issueID, blockers)
				failedCount++
				continue
			}
		}

		params := domain.CloseIssueParams{Reason: reason}
		if isWisp {
			_, err = uw.IssueUseCase().CloseWisp(ctx, issueID, params, actorName)
		} else {
			_, err = uw.IssueUseCase().CloseIssue(ctx, issueID, params, actorName)
		}
		if err != nil {
			reportItemError("Error: failed to close %s: %v", issueID, err)
			failedCount++
			continue
		}

		// beads-58kg8 parity: molecule/wisp/template-epic auto-close cascade,
		// staged into this UOW BEFORE the commit so the root-close lands in the
		// same commit; its audit-file entry is emitted post-commit below.
		if rootID := autoCloseProxiedCompletedMolecule(ctx, uw, issueID, actorName, "", jsonOutput); rootID != "" {
			autoClosedRoots = append(autoClosedRoots, autoClosed{rootID: rootID})
		}

		closedIDs = append(closedIDs, issueID)
	}

	if len(closedIDs) > 0 {
		if err := uw.Commit(ctx, fmt.Sprintf("bd: todo done %v", closedIDs)); err != nil && !isDoltNothingToCommit(err) {
			return HandleErrorRespectJSON("failed to commit: %v", err)
		}
		commandDidWrite.Store(true)

		// beads-58kg8/n4sn parity: GC-survivable audit-file trail for each closed
		// TODO, emitted AFTER the commit (audit.LogFieldChange writes a cwd FILE,
		// not a UOW op — a pre-commit emit would orphan the entry if the deferred
		// uw.Close rolled the close back). oldStatus is "open" (a re-close of an
		// already-closed issue would have been an AlreadyClosed no-op; the direct
		// path assumes open here too).
		for _, id := range closedIDs {
			auditStatusChange(id, "open", "closed", actorName, reason)
		}
		// beads-jcrp4 parity: the auto-closed molecule/wisp root's own audit-file
		// entry, also post-commit.
		for _, ac := range autoClosedRoots {
			auditStatusChange(ac.rootID, "open", "closed", actorName, "all steps complete")
		}
	}

	if jsonOutput {
		// beads-s2oy parity: outputJSON for schema_version + BD_JSON_ENVELOPE, and
		// the exact direct-path shape {"closed":[...],"reason":...}.
		if err := outputJSON(map[string]interface{}{
			"closed": closedIDs,
			"reason": reason,
		}); err != nil {
			return err
		}
		// beads-xi35 parity: signal non-zero if any id failed so scripted
		// `bd todo done $ids || ...` guards fire.
		if failedCount > 0 {
			return &exitError{Code: 1}
		}
		return nil
	}
	for _, id := range closedIDs {
		fmt.Printf("Closed %s\n", ui.RenderID(id))
	}
	if failedCount > 0 {
		return &exitError{Code: 1}
	}
	return nil
}
