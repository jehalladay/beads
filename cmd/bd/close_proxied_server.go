package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/hooks"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

type closeProxiedInput struct {
	force       bool
	continueOn  bool
	noAuto      bool
	suggestNext bool
	claimNext   bool
	session     string
	jsonOut     bool
}

type closeProxiedOutcome struct {
	id     string
	before *types.Issue
	after  *types.Issue
	closed bool
	// autoClosedRoot is the molecule/wisp root auto-closed by the cascade for
	// this step ("" if none) — its GC-survivable audit-file entry is emitted
	// post-commit (beads-jcrp4), same as the step's own close audit.
	autoClosedRoot string
}

func runCloseProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) {
	if len(args) == 0 {
		FatalErrorRespectJSON("no issue ID provided")
	}

	reasons, updatedArgs, err := resolveCloseReasons(cmd, args)
	if err != nil {
		FatalErrorRespectJSON("%v", err)
	}
	args = updatedArgs
	if err := validateCloseReasons(reasons); err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	in := gatherCloseProxiedInput(cmd)

	if in.continueOn && len(args) > 1 {
		FatalErrorRespectJSON("--continue only works when closing a single issue")
	}
	if in.suggestNext && len(args) > 1 {
		FatalErrorRespectJSON("--suggest-next only works when closing a single issue")
	}

	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		FatalErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	outcomes := make([]closeProxiedOutcome, 0, len(args))
	closedIssues := []*types.Issue{}
	// beads-2j2og: buffer per-item guard/error messages under --json so they can
	// flush as JSON stderr objects alongside a partial-success stdout array
	// (mirrors the direct path's reportCloseItemError, cmd/bd/close.go). Non-JSON
	// keeps immediate plain stderr. On a wholly-failed batch the terminal
	// FatalErrorRespectJSON is the sole error (stderr stays clean), so the buffer
	// is intentionally NOT flushed there.
	var deferredItemErrors []string
	reportCloseItemError := func(format string, a ...interface{}) {
		if in.jsonOut {
			deferredItemErrors = append(deferredItemErrors, fmt.Sprintf(format, a...))
			return
		}
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
	for i, id := range args {
		reason := reasonForCloseIndex(reasons, i)
		outcome, ok := closeProxiedOne(ctx, uw, id, reason, in, reportCloseItemError)
		if !ok {
			continue
		}
		outcomes = append(outcomes, outcome)
		if in.jsonOut {
			closedIssues = append(closedIssues, outcome.after)
		} else if !outcome.closed {
			// beads-8l5t: the issue was ALREADY closed (repo Close is idempotent
			// via AlreadyClosed). Report the no-op honestly instead of claiming a
			// "✓ Closed" transition, matching the direct path (cmd/bd/close.go
			// "was already closed (no change)"). rc stays 0 (idempotent close).
			fmt.Printf("%s %s was already closed (no change)\n",
				ui.RenderInfoIcon(), formatFeedbackID(outcome.after.ID, outcome.after.Title))
		} else {
			fmt.Printf("%s Closed %s: %s\n", ui.RenderPass("✓"), formatFeedbackID(outcome.after.ID, outcome.after.Title), reason)
		}
	}

	var unblocked []*types.Issue
	if in.suggestNext && len(args) == 1 && len(outcomes) > 0 {
		unblocked = closeProxiedSuggestNext(ctx, uw, args[0])
	}

	var continueResult *ContinueResult
	if in.continueOn && len(args) == 1 && len(outcomes) > 0 {
		continueResult = closeProxiedContinue(ctx, uw, args[0], !in.noAuto)
	}

	if len(outcomes) > 0 {
		msg := closeProxiedCommitMessage(outcomes, nil, continueResult)
		if err := uw.Commit(ctx, msg); err != nil && !isDoltNothingToCommit(err) {
			FatalErrorRespectJSON("commit close: %v", err)
		}
		for _, o := range outcomes {
			if !o.closed {
				continue
			}
			// beads-jcrp4: GC-survivable audit-file trail for a molecule/wisp
			// root the cascade auto-closed — emitted AFTER the commit (the
			// root-close is already committed above), at parity with the direct
			// autoCloseCompletedMolecule (beads-zt47w) and the step's own close
			// audit. The root was open (helper guards Status != closed).
			if o.autoClosedRoot != "" {
				auditStatusChange(o.autoClosedRoot, "open", "closed", actor, "all steps complete")
			}
			if err := fireProxiedCloseHooks(ctx, o.before, o.after); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: %v\n", o.id, err)
			}
		}
	}

	// beads-iq8zr: run --claim-next in its OWN UnitOfWork AFTER the primary close
	// has committed. Bundling the opportunistic next-claim into the close commit
	// meant a lost concurrent claim race (Dolt raises 1213 on the shared
	// sql-server) rolled back the WHOLE unit of work — silently reverting the
	// user's actual close. The direct path (close.go) already commits the close
	// first, then claims separately and only warns on failure; mirror that here.
	var claimedNextIssue *types.Issue
	if in.claimNext && len(outcomes) > 0 && !in.continueOn {
		claimedNextIssue = closeProxiedClaimNextSeparate(ctx, in.jsonOut)
	}

	if !in.jsonOut {
		if len(unblocked) > 0 {
			fmt.Printf("\nNewly unblocked:\n")
			for _, issue := range unblocked {
				fmt.Printf("  • %s (P%d)\n", formatFeedbackID(issue.ID, issue.Title), issue.Priority)
			}
		}
		if continueResult != nil {
			PrintContinueResult(continueResult)
		}
		if claimedNextIssue != nil {
			fmt.Printf("%s Auto-claimed next ready issue: %s (P%d)\n", ui.RenderPass("✓"), formatFeedbackID(claimedNextIssue.ID, claimedNextIssue.Title), claimedNextIssue.Priority)
		}
	}

	if in.jsonOut && len(closedIssues) > 0 {
		switch {
		case len(unblocked) > 0:
			_ = outputJSON(map[string]interface{}{"closed": closedIssues, "unblocked": unblocked})
		case continueResult != nil:
			_ = outputJSON(map[string]interface{}{"closed": closedIssues, "continue": continueResult})
		case claimedNextIssue != nil:
			_ = outputJSON(map[string]interface{}{"closed": closedIssues, "claimed": claimedNextIssue})
		default:
			_ = outputJSON(closedIssues)
		}
		// beads-2j2og: partial success — stdout carries the closed[] array, so
		// any deferred per-item failures flush to stderr as JSON objects now
		// rather than being dropped (mirrors close.go's beads-n96g/en28 flush).
		// reportItemError (errors.go) JSON-wraps each message under --json.
		for _, msg := range deferredItemErrors {
			reportItemError("%s", msg)
		}
	}

	// Record last-touched so `bd show --current` fallback works after a proxied
	// close, matching the direct close path (close.go): prefer the auto-claimed
	// next issue (the work you'd continue), else the first closed issue (beads-gw7s).
	switch {
	case claimedNextIssue != nil:
		SetLastTouchedID(claimedNextIssue.ID)
	case len(closedIssues) > 0:
		SetLastTouchedID(closedIssues[0].ID)
	}

	// beads-gt5p: exit non-zero when ANY id failed (not only when ALL failed).
	// closeProxiedOne returns an outcome for every id that closed OR was an
	// idempotent already-closed no-op (rc-0-fine per beads-8l5t), and nothing
	// for a genuine failure (not-found / guard / blocked, already reported to
	// stderr). So len(outcomes) < len(args) means at least one id genuinely
	// failed — mirror the direct path (close.go: genuine failures trip a
	// non-zero exit even alongside successes) and the update/cwl8 partial-exit
	// contract, instead of the old ALL-failed-only check that returned rc=0 on
	// a partial batch (e.g. `bd close good-id ghost-id`) — a false-clean read
	// for a proxied crew scripting `bd close a b c || fail`.
	if len(outcomes) < len(args) {
		// beads-j43d: on a wholly-failed batch (nothing closed) under --json, emit
		// a stdout JSON error object instead of a bare os.Exit(1) with empty stdout
		// — mirrors the direct path (close.go) so a --json consumer can distinguish
		// "command failed" from "produced no output". A partial batch already
		// emitted the JSON above (len(closedIssues) > 0) and keeps the plain exit.
		if in.jsonOut && len(closedIssues) == 0 {
			FatalErrorRespectJSON("no issues closed matching the provided IDs")
		}
		os.Exit(1)
	}
}

func gatherCloseProxiedInput(cmd *cobra.Command) closeProxiedInput {
	in := closeProxiedInput{}
	in.force, _ = cmd.Flags().GetBool("force")
	in.continueOn, _ = cmd.Flags().GetBool("continue")
	in.noAuto, _ = cmd.Flags().GetBool("no-auto")
	in.suggestNext, _ = cmd.Flags().GetBool("suggest-next")
	in.claimNext, _ = cmd.Flags().GetBool("claim-next")
	in.session, _ = cmd.Flags().GetString("session")
	if in.session == "" {
		in.session = os.Getenv("CLAUDE_SESSION_ID")
	}
	in.jsonOut, _ = cmd.Flags().GetBool("json")
	return in
}

// closeProxiedOne closes a single issue via the UOW. Per-item guard/error
// messages are routed through report (beads-2j2og), which buffers them under
// --json (flushed as JSON stderr objects by the caller on partial success) and
// prints immediate plain stderr otherwise — mirroring the direct path's
// reportCloseItemError (cmd/bd/close.go). Previously these legs wrote bare
// plaintext to os.Stderr regardless of --json, so a proxied-mode consumer got
// un-parseable per-item failures.
func closeProxiedOne(ctx context.Context, uw uow.UnitOfWork, id, reason string, in closeProxiedInput, report func(format string, a ...interface{})) (closeProxiedOutcome, bool) {
	current, isWisp := proxiedResolveIssueOrWisp(ctx, uw, id)
	if current == nil {
		report("Issue %s not found", id)
		return closeProxiedOutcome{}, false
	}

	if err := validateIssueClosable(id, current, in.force); err != nil {
		report("%s", err)
		return closeProxiedOutcome{}, false
	}

	// beads-bigro: forward close-time guard mirrors the direct path
	// (close.go) — use the shared isAutoClosingParentType predicate (epic OR
	// molecule/ephemeral root) that aw9x8 wired into the backward guards, so a
	// molecule/wisp root cannot be manually closed with open children on the
	// proxied path either.
	if !in.force && isAutoClosingParentType(current) {
		var openChildren int
		var err error
		if isWisp {
			openChildren, err = uw.IssueUseCase().CountOpenWispChildren(ctx, id)
		} else {
			openChildren, err = uw.IssueUseCase().CountOpenChildren(ctx, id)
		}
		if err == nil && openChildren > 0 {
			report("cannot close %s: %d open child issue(s); close children first or use --force to override", id, openChildren)
			return closeProxiedOutcome{}, false
		}
	}

	if !in.force {
		if err := checkGateSatisfaction(current); err != nil {
			// beads-pbt8m: sanitize the gate-satisfaction error for display —
			// for a gh:pr / gh:run gate it embeds an UNTRUSTED PR title /
			// workflow name that can carry OSC/CSI terminal-injection escapes
			// (7n9y sink; proxied twin of the close.go:170 fix). Display-only.
			report("cannot close %s: %s", id, ui.SanitizeForTerminal(err.Error()))
			return closeProxiedOutcome{}, false
		}
	}

	if !in.force {
		var blocked bool
		var blockers []string
		var err error
		if isWisp {
			blocked, blockers, err = uw.DependencyUseCase().IsWispBlocked(ctx, id)
		} else {
			blocked, blockers, err = uw.DependencyUseCase().IsBlocked(ctx, id)
		}
		if err != nil {
			report("Error checking blockers for %s: %v", id, err)
			return closeProxiedOutcome{}, false
		}
		if blocked && len(blockers) > 0 {
			report("cannot close %s: blocked by open issues %v (use --force to override)", id, blockers)
			return closeProxiedOutcome{}, false
		}
	}

	params := domain.CloseIssueParams{Reason: reason, Session: in.session}
	var (
		res domain.CloseIssueResult
		err error
	)
	if isWisp {
		res, err = uw.IssueUseCase().CloseWisp(ctx, id, params, actor)
	} else {
		res, err = uw.IssueUseCase().CloseIssue(ctx, id, params, actor)
	}
	if err != nil {
		report("Error closing %s: %v", id, err)
		return closeProxiedOutcome{}, false
	}

	// beads-8l5t: only emit the open→closed audit event on a REAL transition.
	// The repo Close is idempotent (res.Closed == !AlreadyClosed); re-closing an
	// already-closed issue must not pollute the GC-survivable audit trail with a
	// transition that never happened (matches the direct path, which skips the
	// close work entirely when already closed; beads-usz1 class).
	if res.Closed {
		oldStatus := string(current.Status)
		if oldStatus == "" {
			oldStatus = "open"
		}
		auditStatusChange(id, oldStatus, "closed", actor, reason)
	}

	autoClosedRoot := autoCloseProxiedCompletedMolecule(ctx, uw, id, actor, in.session, in.jsonOut)

	return closeProxiedOutcome{id: id, before: current, after: res.Issue, closed: res.Closed, autoClosedRoot: autoClosedRoot}, true
}

func closeProxiedCommitMessage(outcomes []closeProxiedOutcome, claimed *types.Issue, cont *ContinueResult) string {
	ids := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		ids = append(ids, o.id)
	}
	msg := "bd: close " + strings.Join(ids, ", ")
	if cont != nil && cont.AutoAdvanced && cont.NextStep != nil {
		msg += "; advance to " + cont.NextStep.ID
	}
	if claimed != nil {
		msg += "; claim " + claimed.ID
	}
	return msg
}

func proxiedResolveIssueOrWisp(ctx context.Context, uw uow.UnitOfWork, id string) (*types.Issue, bool) {
	issue, err := uw.IssueUseCase().GetIssue(ctx, id)
	if err == nil && issue != nil {
		return issue, false
	}
	wisp, err := uw.IssueUseCase().GetWisp(ctx, id)
	if err == nil && wisp != nil {
		return wisp, true
	}
	return nil, false
}

func fireProxiedCloseHooks(ctx context.Context, before, after *types.Issue) error {
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
	if before != nil && before.Status != types.StatusClosed && after.Status == types.StatusClosed {
		if err := runner.RunSync(hooks.EventClose, after); err != nil {
			return fmt.Errorf("on_close hook: %w", err)
		}
	}
	return nil
}

func closeProxiedSuggestNext(ctx context.Context, uw uow.UnitOfWork, closedID string) []*types.Issue {
	unblocked, err := uw.IssueUseCase().GetNewlyUnblockedByClose(ctx, closedID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not compute newly unblocked: %v\n", err)
		return nil
	}
	return unblocked
}

// closeProxiedClaimNextSeparate claims the next ready issue in a SEPARATE
// UnitOfWork, run only after the primary close has already committed. It never
// fataly errors: any failure (including a lost concurrent claim race) is a
// non-fatal warning, so it cannot revert the close (beads-iq8zr). The whole
// read-claim-commit critical section is serialized by the same server-scoped
// advisory lock as `bd ready --claim` so two concurrent claimers cannot
// double-claim the same issue (beads-1i4u): both handlers take "bd_ready_claim",
// so close --claim-next and ready --claim are mutually exclusive too.
func closeProxiedClaimNextSeparate(ctx context.Context, jsonOut bool) *types.Issue {
	if uowProvider == nil {
		return nil
	}

	if locker, ok := uowProvider.(interface {
		AcquireAdvisoryLock(ctx context.Context, name string, timeoutSeconds int) (func(), error)
	}); ok {
		release, lockErr := locker.AcquireAdvisoryLock(ctx, "bd_ready_claim", 10)
		if lockErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not serialize claim-next: %v\n", lockErr)
			return nil
		}
		defer release()
	}

	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not open claim-next unit of work: %v\n", err)
		return nil
	}
	defer uw.Close(ctx)

	page, err := uw.IssueUseCase().GetReadyWork(ctx, types.WorkFilter{
		Status:     "open",
		Limit:      1,
		SortPolicy: types.SortPolicy("priority"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not get ready issues: %v\n", err)
		return nil
	}
	if len(page.Items) == 0 {
		if !jsonOut {
			fmt.Printf("\n%s No ready issues available to claim.\n", ui.RenderWarn("✨"))
		}
		return nil
	}

	nextIssue := page.Items[0]
	if _, err := uw.IssueUseCase().ClaimIssue(ctx, nextIssue.ID, actor); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not claim next issue %s: %v\n", nextIssue.ID, err)
		return nil
	}
	if err := uw.Commit(ctx, "bd: claim "+nextIssue.ID); err != nil && !isDoltNothingToCommit(err) {
		fmt.Fprintf(os.Stderr, "Warning: could not commit claim of next issue %s: %v\n", nextIssue.ID, err)
		return nil
	}
	// beads-yt2hi: nextIssue is the PRE-claim snapshot (status:open, no
	// assignee/started_at); the mutated row must be re-read so the --json
	// `claimed` object reflects the persisted in_progress+assignee state,
	// mirroring the direct path. Fall back to the snapshot on a re-read hiccup.
	if refreshed, rerr := uw.IssueUseCase().GetIssue(ctx, nextIssue.ID); rerr == nil && refreshed != nil {
		return refreshed
	}
	return nextIssue
}

func closeProxiedContinue(ctx context.Context, uw uow.UnitOfWork, closedID string, autoClaim bool) *ContinueResult {
	result, err := proxiedAdvanceToNextStep(ctx, uw, closedID, autoClaim, actor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not advance to next step: %v\n", err)
		return nil
	}
	return result
}

// autoCloseProxiedCompletedMolecule stages the auto-close of a completed
// molecule/wisp root into the caller's UOW (BEFORE its uw.Commit, so the
// root-close lands in the same commit). It RETURNS the closed root's id (""
// if none) so the caller can emit the GC-survivable audit-file trail for that
// close AFTER its uw.Commit succeeds (beads-jcrp4): audit.LogFieldChange writes
// a cwd FILE, not a UOW op, so a pre-commit emit here would orphan the entry if
// the deferred uw.Close rolled the staged root-close back — the same
// post-commit ordering the source-close audit uses (r3m8v/c2pr1 lesson).
func autoCloseProxiedCompletedMolecule(ctx context.Context, uw uow.UnitOfWork, closedStepID string, actorName, session string, jsonOut bool) string {
	moleculeID := proxiedFindParentMolecule(ctx, uw, closedStepID)
	if moleculeID == "" {
		return ""
	}

	root, err := uw.IssueUseCase().GetIssue(ctx, moleculeID)
	if err != nil || root == nil || root.Status == types.StatusClosed {
		return ""
	}
	if labels, err := uw.LabelUseCase().GetLabels(ctx, moleculeID); err == nil {
		root.Labels = labels
	}
	if !shouldAutoCloseCompletedRoot(root) {
		return ""
	}

	progress, err := proxiedGetMoleculeProgress(ctx, uw, moleculeID)
	if err != nil {
		return ""
	}
	if progress.Completed < progress.Total {
		return ""
	}

	params := domain.CloseIssueParams{Reason: "all steps complete", Session: session}
	if _, err := uw.IssueUseCase().CloseIssue(ctx, moleculeID, params, actorName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not auto-close completed molecule %s: %v\n", moleculeID, err)
		return ""
	}
	if !jsonOut {
		fmt.Printf("%s Auto-closed completed molecule %s\n", ui.RenderPass("✓"), formatFeedbackID(moleculeID, root.Title))
	}
	return moleculeID
}

func proxiedFindParentMolecule(ctx context.Context, uw uow.UnitOfWork, issueID string) string {
	current := issueID
	for depth := 0; depth < 50; depth++ {
		deps, err := uw.DependencyUseCase().GetForIssueIDs(ctx, []string{current})
		if err != nil {
			return ""
		}
		var parent string
		for _, dep := range deps[current] {
			if dep.Type == types.DepParentChild {
				parent = dep.DependsOnID
				break
			}
		}
		if parent == "" {
			if current == issueID {
				return ""
			}
			return current
		}
		current = parent
	}
	return current
}

func proxiedLoadTemplateSubgraph(ctx context.Context, uw uow.UnitOfWork, templateID string) (*TemplateSubgraph, error) {
	root, err := uw.IssueUseCase().GetIssue(ctx, templateID)
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("template %s not found", templateID)
	}

	subgraph := &TemplateSubgraph{
		Root:     root,
		Issues:   []*types.Issue{root},
		IssueMap: map[string]*types.Issue{root.ID: root},
	}

	visited := map[string]bool{root.ID: true}
	if err := proxiedLoadDescendants(ctx, uw, subgraph, root.ID, visited); err != nil {
		return nil, err
	}

	for _, issue := range subgraph.Issues {
		deps, err := uw.DependencyUseCase().GetForIssueIDs(ctx, []string{issue.ID})
		if err != nil {
			return nil, fmt.Errorf("failed to get dependencies for %s: %w", issue.ID, err)
		}
		for _, dep := range deps[issue.ID] {
			if _, ok := subgraph.IssueMap[dep.DependsOnID]; ok {
				subgraph.Dependencies = append(subgraph.Dependencies, dep)
			}
		}
	}

	return subgraph, nil
}

func proxiedLoadDescendants(ctx context.Context, uw uow.UnitOfWork, subgraph *TemplateSubgraph, parentID string, visited map[string]bool) error {
	dependents, err := uw.DependencyUseCase().ListWithIssueMetadata(ctx, parentID, domain.DepListFilter{
		Direction: domain.DepDirectionIn,
	})
	if err != nil {
		return fmt.Errorf("failed to get dependents of %s: %w", parentID, err)
	}

	for _, dependent := range dependents {
		if dependent.DependencyType != types.DepParentChild {
			continue
		}
		if _, exists := subgraph.IssueMap[dependent.ID]; exists {
			continue
		}
		if visited[dependent.ID] {
			continue
		}
		child := dependent.Issue
		subgraph.Issues = append(subgraph.Issues, &child)
		subgraph.IssueMap[child.ID] = &child
		visited[child.ID] = true
		if err := proxiedLoadDescendants(ctx, uw, subgraph, child.ID, visited); err != nil {
			return err
		}
	}
	return nil
}

func proxiedGetMoleculeProgress(ctx context.Context, uw uow.UnitOfWork, moleculeID string) (*MoleculeProgress, error) {
	subgraph, err := proxiedLoadTemplateSubgraph(ctx, uw, moleculeID)
	if err != nil {
		return nil, err
	}

	progress := &MoleculeProgress{
		MoleculeID:    subgraph.Root.ID,
		MoleculeTitle: subgraph.Root.Title,
		Assignee:      subgraph.Root.Assignee,
		Total:         len(subgraph.Issues) - 1,
	}

	analysis := analyzeMoleculeParallel(subgraph)
	readyIDs := make(map[string]bool)
	for id, info := range analysis.Steps {
		if info.IsReady {
			readyIDs[id] = true
		}
	}

	var steps []*StepStatus
	for _, issue := range subgraph.Issues {
		if issue.ID == subgraph.Root.ID {
			continue
		}
		step := &StepStatus{Issue: issue}
		switch issue.Status {
		case types.StatusClosed:
			step.Status = "done"
			progress.Completed++
		case types.StatusInProgress:
			step.Status = "current"
			step.IsCurrent = true
			progress.CurrentStep = issue
		case types.StatusBlocked:
			step.Status = "blocked"
		default:
			if readyIDs[issue.ID] {
				step.Status = "ready"
				if progress.NextStep == nil {
					progress.NextStep = issue
				}
			} else {
				step.Status = "pending"
			}
		}
		steps = append(steps, step)
	}

	sortStepsByDependencyOrder(steps, subgraph)
	progress.Steps = steps

	if progress.CurrentStep == nil && progress.NextStep == nil {
		for _, step := range steps {
			if step.Status == "ready" {
				progress.NextStep = step.Issue
				break
			}
		}
	}

	return progress, nil
}

func proxiedAdvanceToNextStep(ctx context.Context, uw uow.UnitOfWork, closedStepID string, autoClaim bool, actorName string) (*ContinueResult, error) {
	closedStep, err := uw.IssueUseCase().GetIssue(ctx, closedStepID)
	if err != nil || closedStep == nil {
		wisp, wErr := uw.IssueUseCase().GetWisp(ctx, closedStepID)
		if wErr != nil || wisp == nil {
			return nil, fmt.Errorf("could not get closed step: %w", err)
		}
		closedStep = wisp
	}

	result := &ContinueResult{ClosedStep: closedStep}

	moleculeID := proxiedFindParentMolecule(ctx, uw, closedStepID)
	if moleculeID == "" {
		return nil, nil
	}
	result.MoleculeID = moleculeID

	progress, err := proxiedGetMoleculeProgress(ctx, uw, moleculeID)
	if err != nil {
		return nil, fmt.Errorf("could not load molecule: %w", err)
	}

	if progress.Completed >= progress.Total {
		result.MolComplete = true
		return result, nil
	}

	var readySteps []*types.Issue
	for _, step := range progress.Steps {
		if step.Status == "ready" {
			readySteps = append(readySteps, step.Issue)
		}
	}
	if len(readySteps) == 0 {
		return result, nil
	}
	result.NextStep = readySteps[0]

	if !autoClaim {
		return result, nil
	}

	for _, candidate := range readySteps {
		_, claimErr := uw.IssueUseCase().ClaimIssueIfOpen(ctx, candidate.ID, actorName)
		if claimErr == nil {
			result.NextStep = candidate
			result.AutoAdvanced = true
			// beads-7vvyi: `candidate` is the pre-claim progress snapshot
			// (status:open, no started_at). Re-read so result.NextStep reflects
			// the persisted in_progress+started_at state a --json consumer checks
			// to confirm the auto-advance landed; keep the snapshot on a hiccup.
			if claimed, rErr := uw.IssueUseCase().GetIssue(ctx, candidate.ID); rErr == nil && claimed != nil {
				result.NextStep = claimed
			}
			return result, nil
		}
		if errors.Is(claimErr, storage.ErrAlreadyClaimed) || errors.Is(claimErr, storage.ErrNotClaimable) {
			continue
		}
		return result, nil
	}
	return result, nil
}
