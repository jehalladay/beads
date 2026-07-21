package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// gateCmd is the parent command for gate operations
var gateCmd = &cobra.Command{
	Use:     "gate",
	GroupID: "issues",
	Short:   "Manage async coordination gates",
	Long: `Gates are async wait conditions that block workflow steps.

Gates are created automatically when a formula step has a gate field.
They must be closed (manually or via watchers) for the blocked step to proceed.

Gate types:
  human   - Requires manual bd close (Phase 1)
  timer   - Expires after timeout (Phase 2)
  gh:run  - Waits for GitHub workflow (Phase 3)
  gh:pr   - Waits for PR merge (Phase 3)

Examples:
  bd gate list           # Show all open gates
  bd gate list --all     # Show all gates including closed
  bd gate check          # Evaluate all open gates
  bd gate check --type=timer # Evaluate only timer gates
  bd gate resolve <id>   # Close a gate manually`,
}

// gateListCmd lists gate issues
var gateListCmd = &cobra.Command{
	Use:   "list",
	Args:  cobra.NoArgs, // beads-8jy7e: reject stray positionals with a clean usage error
	Short: "List gate issues",
	Long: `List all gate issues in the current beads database.

By default, shows only open gates. Use --all to include closed gates.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("gate-list")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		allFlag, _ := cmd.Flags().GetBool("all")
		limit, _ := cmd.Flags().GetInt("limit")
		// Reject a negative --limit up front (beads-eqi4): the SQL builders
		// only apply filter.Limit when >0, so a negative value silently returns
		// the full set. Shared with bd list (uh4i) via validateLimitFromCmd.
		if err := validateLimitFromCmd(cmd); err != nil {
			return err
		}

		gateType := types.IssueType("gate")
		filter := types.IssueFilter{
			IssueType: &gateType,
			Limit:     limit,
		}

		if !allFlag {
			filter.ExcludeStatus = []types.Status{types.StatusClosed}
		}

		ctx := rootCtx

		if usesProxiedServer() {
			return runGateListProxied(ctx, filter, allFlag)
		}

		issues, err := store.SearchIssues(ctx, "", filter)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		if jsonOutput {
			// beads-tamf: normalize nil→[] so an empty result marshals to a JSON
			// [] (not null), matching the ready/blocked/list array contract.
			if issues == nil {
				issues = []*types.Issue{}
			}
			return outputJSON(issues)
		}

		displayGates(issues, allFlag)
		return nil
	},
}

// displayGates formats and displays gate issues, separating open and closed gates
func displayGates(gates []*types.Issue, showAll bool) {
	if len(gates) == 0 {
		fmt.Println("No gates found.")
		return
	}

	// Separate open and closed gates
	var openGates, closedGates []*types.Issue
	for _, gate := range gates {
		if gate.Status == types.StatusClosed {
			closedGates = append(closedGates, gate)
		} else {
			openGates = append(openGates, gate)
		}
	}

	// Display open gates
	if len(openGates) > 0 {
		fmt.Printf("\n%s Open Gates (%d):\n\n", ui.RenderAccent("⏳"), len(openGates))
		for _, gate := range openGates {
			displaySingleGate(gate)
		}
	}

	// Display closed gates only if --all was used
	if showAll && len(closedGates) > 0 {
		fmt.Printf("\n%s Closed Gates (%d):\n\n", ui.RenderMuted("●"), len(closedGates))
		for _, gate := range closedGates {
			displaySingleGate(gate)
		}
	}

	if len(openGates) == 0 && (!showAll || len(closedGates) == 0) {
		fmt.Println("No gates found.")
		return
	}

	fmt.Printf("To resolve a gate: bd close <gate-id>\n")
}

// displaySingleGate formats and displays a single gate issue
func displaySingleGate(gate *types.Issue) {
	statusSym := "○"
	if gate.Status == types.StatusClosed {
		statusSym = "●"
	}

	// Format gate info
	gateInfo := gate.AwaitType
	if gate.AwaitID != "" {
		gateInfo = fmt.Sprintf("%s %s", gate.AwaitType, gate.AwaitID)
	}

	// Format timeout if present
	timeoutStr := ""
	if gate.Timeout > 0 {
		timeoutStr = fmt.Sprintf(" (timeout: %s)", gate.Timeout)
	}

	// Find blocked step from ID (gate ID format: parent.gate-stepid)
	blockedStep := ""
	if strings.Contains(gate.ID, ".gate-") {
		parts := strings.Split(gate.ID, ".gate-")
		if len(parts) == 2 {
			blockedStep = fmt.Sprintf("%s.%s", parts[0], parts[1])
		}
	}

	fmt.Printf("%s %s - %s%s\n", statusSym, ui.RenderID(gate.ID), gateInfo, timeoutStr)
	if blockedStep != "" {
		fmt.Printf("  Blocks: %s\n", blockedStep)
	}
	fmt.Println()
}

// gateAddWaiterCmd adds a waiter to a gate
var gateAddWaiterCmd = &cobra.Command{
	Use:   "add-waiter <gate-id> <waiter>",
	Short: "Add a waiter to a gate",
	Long: `Register an agent as a waiter on a gate bead.

When the gate closes, the waiter will receive a wake notification via 'bd gate wake'.
The waiter is typically the worker's address (e.g., "my-project/workers/agent-1").

This is used by 'bd done --phase-complete' to register for gate wake notifications.`,
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("gate add-waiter")

		evt := metrics.NewCommandEvent("gate-add-waiter")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		gateID := args[0]
		waiter := args[1]
		ctx := rootCtx

		if usesProxiedServer() {
			return runGateAddWaiterProxied(ctx, gateID, waiter)
		}

		var issue *types.Issue
		var err error

		issue, err = store.GetIssue(ctx, gateID)
		if err != nil {
			// beads-jial: honor the --json error contract on the direct-path
			// guards, matching the sibling 'gate resolve' (beads-u3lt) and
			// 'gate create'. A bare HandleError left stdout empty + stderr
			// plaintext under `bd gate add-waiter <bad-id> --json`.
			return HandleErrorRespectJSON("gate not found: %s", gateID)
		}

		if issue.IssueType != "gate" {
			return HandleErrorRespectJSON("%s is not a gate issue (type=%s)", gateID, issue.IssueType)
		}

		for _, w := range issue.Waiters {
			if w == waiter {
				// beads-w17gk: honor --json on the idempotent no-op success
				// leg, matching this command's error legs (beads-jial) and the
				// sibling 'gate resolve' already-resolved no-op (beads-q2iw4).
				if jsonOutput {
					return outputJSON(map[string]interface{}{
						"gate":   gateID,
						"waiter": waiter,
						"added":  false,
					})
				}
				fmt.Printf("Waiter already registered on gate %s\n", gateID)
				return nil
			}
		}

		newWaiters := append(issue.Waiters, waiter)

		updates := map[string]interface{}{
			"waiters": newWaiters,
		}
		if err := store.UpdateIssue(ctx, gateID, updates, actor); err != nil {
			return HandleErrorRespectJSON("updating gate: %v", err)
		}

		commandDidWrite.Store(true)

		// beads-w17gk: emit a JSON object on the first-add success leg under
		// --json (the error legs already do via beads-jial) so a --json
		// consumer parsing stdout never hits bare plaintext.
		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"gate":   gateID,
				"waiter": waiter,
				"added":  true,
			})
		}

		fmt.Printf("%s Added waiter to gate %s: %s\n", ui.RenderPass("✓"), gateID, waiter)
		return nil
	},
}

// gateCreateCmd creates an ad-hoc gate issue that blocks another issue
var gateCreateCmd = &cobra.Command{
	Use:   "create",
	Args:  cobra.NoArgs, // beads-8jy7e: reject stray positionals with a clean usage error
	Short: "Create a gate that blocks an issue",
	Long: `Create an ad-hoc gate issue that blocks another issue until resolved.

The blocked issue will not appear in 'bd ready' until the gate is resolved
via 'bd gate resolve'.

Gate types:
  human   - Requires manual 'bd gate resolve' (default)
  timer   - Auto-resolves after --timeout duration
  gh:run  - Waits for GitHub Actions workflow
  gh:pr   - Waits for PR merge

Examples:
  bd gate create --blocks bd-abc
  bd gate create --type=human --blocks bd-abc --reason="Need design review"
  bd gate create --type=timer --blocks bd-abc --timeout=2h
  bd gate create --type=gh:pr --blocks bd-abc --await-id=42`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("gate create")

		evt := metrics.NewCommandEvent("gate-create")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		blocksID, _ := cmd.Flags().GetString("blocks")
		gateType, _ := cmd.Flags().GetString("type")
		reason, _ := cmd.Flags().GetString("reason")
		awaitID, _ := cmd.Flags().GetString("await-id")
		timeoutStr, _ := cmd.Flags().GetString("timeout")
		// beads-57f51: a whitespace-only --reason must collapse to no-reason so it
		// does not leave a dangling "\n\nReason:   " label in the gate description
		// (and no dangling "Reason:  " print). Optional, no default → drop
		// whitespace-only; keep a genuine reason VERBATIM (beads-beln6). Same
		// in93a stored-blank-reason shape as gate resolve + set-state below.
		if strings.TrimSpace(reason) == "" {
			reason = ""
		}

		ctx := rootCtx

		if usesProxiedServer() {
			return runGateCreateProxied(ctx, blocksID, gateType, reason, awaitID, timeoutStr)
		}

		targetIssue, err := store.GetIssue(ctx, blocksID)
		if err != nil {
			return HandleErrorRespectJSON("issue not found: %s", blocksID)
		}

		var timeout time.Duration
		if timeoutStr != "" {
			parsed, err := time.ParseDuration(timeoutStr)
			if err != nil {
				return HandleErrorRespectJSON("invalid timeout: %v", err)
			}
			timeout = parsed
		}

		// beads-ds9tr: validate the gate-type INVARIANTS before create, not just
		// the timeout format. Otherwise create accepts an unresolvable gate (a
		// timer with no timeout, or an unknown type gate check silently skips),
		// stranding the blocked issue out of bd ready forever with manual close
		// the only escape.
		if verr := validateGateCreate(gateType, awaitID, timeoutStr); verr != nil {
			return HandleErrorRespectJSON("%v", verr)
		}

		title := fmt.Sprintf("Gate: %s", gateType)
		if awaitID != "" {
			title = fmt.Sprintf("Gate: %s %s", gateType, awaitID)
		}

		desc := fmt.Sprintf("Ad-hoc gate blocking %s", targetIssue.ID)
		if reason != "" {
			desc = fmt.Sprintf("%s\n\nReason: %s", desc, reason)
		}

		gate := &types.Issue{
			Title:       title,
			Description: desc,
			Status:      types.StatusOpen,
			Priority:    2,
			IssueType:   types.IssueType("gate"),
			AwaitType:   gateType,
			AwaitID:     awaitID,
			Timeout:     timeout,
			CreatedBy:   getActorWithGit(),
			Owner:       getOwner(),
		}

		// beads-tvinu: wrap the create-gate + blocking-dependency sequence in a
		// single transaction so it is all-or-nothing, mirroring the atomic
		// PROXIED twin (runGateCreateProxied buffers CreateIssue + AddDependency
		// on one UnitOfWork + a single uw.Commit) and the pdzyv/graph_apply
		// RunInTransaction precedent. Without the tx, store.CreateIssue autocommits
		// the gate issue internally (GH#2009) while AddDependency only touches the
		// working set until the explicit store.Commit — so a hard failure on
		// AddDependency (or a crash before the Commit) left the gate DURABLY
		// created but not blocking its target: an orphan gate that gates nothing,
		// while the target issue stays out of `bd ready` on no dependency at all
		// (defeating the entire purpose of `bd gate create`). Exact ary2n/pdzyv
		// signature. The commit message references the target (not gate.ID, which
		// is minted inside the tx) — matching the graph_apply minted-ID-in-tx
		// precedent.
		if err := store.RunInTransaction(ctx, fmt.Sprintf("bd: create gate blocking %s", targetIssue.ID), func(tx storage.Transaction) error {
			if err := tx.CreateIssue(ctx, gate, actor); err != nil {
				return fmt.Errorf("creating gate: %w", err)
			}

			dep := &types.Dependency{
				IssueID:     targetIssue.ID,
				DependsOnID: gate.ID,
				Type:        types.DepBlocks,
			}
			if err := tx.AddDependency(ctx, dep, actor); err != nil {
				return fmt.Errorf("adding blocking dependency: %w", err)
			}
			return nil
		}); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		// RunInTransaction commits atomically; mark the explicit commit so the
		// deferred maybeAutoCommit does not double-commit (mirrors the
		// pdzyv/swarm atomic-twin fixes).
		commandDidExplicitDoltCommit = true

		if jsonOutput {
			return outputJSON(gate)
		}

		fmt.Printf("%s Created gate %s (type: %s)\n", ui.RenderPass("✓"), ui.RenderID(gate.ID), gateType)
		// beads-mvb6a: sanitize the target title for terminal display (7n9y
		// holdout) — targetIssue is store-read and an untrusted imported title
		// can carry OSC/CSI escapes; matches the sibling gate.go:415.
		fmt.Printf("  Blocks: %s (%s)\n", targetIssue.ID, displayTitle(targetIssue.Title))
		if reason != "" {
			fmt.Printf("  Reason: %s\n", reason)
		}
		if timeout > 0 {
			fmt.Printf("  Timeout: %s\n", timeout)
		}
		fmt.Printf("\nResolve with: bd gate resolve %s\n", gate.ID)
		return nil
	},
}

// gateShowCmd shows a gate issue
var gateShowCmd = &cobra.Command{
	Use:   "show <gate-id>",
	Short: "Show a gate issue",
	Long: `Display details of a gate issue including its waiters.

This is similar to 'bd show' but validates that the issue is a gate.`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("gate-show")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		gateID := args[0]
		ctx := rootCtx

		if usesProxiedServer() {
			return runGateShowProxied(ctx, gateID)
		}

		var issue *types.Issue
		var err error

		issue, err = store.GetIssue(ctx, gateID)
		if err != nil {
			return HandleErrorRespectJSON("gate not found: %s", gateID)
		}

		if issue.IssueType != "gate" {
			return HandleErrorRespectJSON("%s is not a gate issue (type=%s)", gateID, issue.IssueType)
		}

		if jsonOutput {
			return outputJSON(issue)
		}

		statusSym := "○"
		if issue.Status == types.StatusClosed {
			statusSym = "●"
		}

		fmt.Printf("%s %s - %s\n", statusSym, ui.RenderID(issue.ID), ui.SanitizeForTerminal(issue.Title))
		fmt.Printf("  Status: %s\n", issue.Status)
		fmt.Printf("  Await Type: %s\n", issue.AwaitType)
		if issue.AwaitID != "" {
			fmt.Printf("  Await ID: %s\n", issue.AwaitID)
		}
		if issue.Timeout > 0 {
			fmt.Printf("  Timeout: %s\n", issue.Timeout)
		}
		if len(issue.Waiters) > 0 {
			fmt.Printf("  Waiters:\n")
			for _, w := range issue.Waiters {
				fmt.Printf("    - %s\n", w)
			}
		}
		if issue.Description != "" {
			fmt.Printf("  Description: %s\n", ui.SanitizeForTerminal(issue.Description))
		}
		return nil
	},
}

// gateAlreadyResolved reports whether a gate issue is already resolved (closed),
// so `bd gate resolve` can emit an idempotent no-op notice instead of a false
// "✓ Gate resolved" that contradicts the stored state (beads-q2iw4). Shared by
// the direct (gateResolveCmd) and proxied (runGateResolveProxied) entry points
// so both stay in lockstep — the store's CloseIssue is a no-op on an
// already-closed issue, so a fresh success claim (with the NEW reason) is a lie.
func gateAlreadyResolved(issue *types.Issue) bool {
	return issue != nil && issue.Status == types.StatusClosed
}

// gateResolveCmd manually closes a gate
var gateResolveCmd = &cobra.Command{
	Use:   "resolve <gate-id>",
	Short: "Manually resolve (close) a gate",
	Long: `Close a gate issue to unblock the step waiting on it.

This is equivalent to 'bd close <gate-id>' but with a more explicit name.
Use --reason to provide context for why the gate was resolved.`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("gate resolve")

		evt := metrics.NewCommandEvent("gate-resolve")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		gateID := args[0]
		reason, _ := cmd.Flags().GetString("reason")
		// beads-57f51: a whitespace-only --reason must collapse to no-reason, not
		// be stored as a blank close_reason on the gate (and not leak into the
		// --json doc or print a dangling "Reason:  " line). gate resolve's reason
		// is OPTIONAL with no default, so mirror reopen/beads-5rix3 semantics:
		// drop a whitespace-only value; keep a genuine reason VERBATIM (beads-beln6).
		// Part of the in93a stored-blank-reason class (close/mol squash/reopen/todo).
		if strings.TrimSpace(reason) == "" {
			reason = ""
		}

		ctx := rootCtx

		if usesProxiedServer() {
			return runGateResolveProxied(ctx, gateID, reason)
		}

		var issue *types.Issue
		var err error

		issue, err = store.GetIssue(ctx, gateID)
		if err != nil {
			return HandleErrorRespectJSON("gate not found: %s", gateID)
		}

		if issue.IssueType != "gate" {
			return HandleErrorRespectJSON("%s is not a gate issue (type=%s)", gateID, issue.IssueType)
		}

		// Already-resolved guard (beads-q2iw4): resolving a gate that is already
		// closed is a no-op — store.CloseIssue silently discards the 2nd close, so
		// printing a fresh "✓ Gate resolved" + the NEW reason (or emitting
		// {"resolved":true,"reason":<new>}) makes the OUTPUT CONTRADICT the STORED
		// STATE. Report it as an informational no-op that reflects the ORIGINAL
		// stored reason instead — mirrors bd close's guard (close.go:145,
		// beads-dr3: "JSON reflects state, not a claimed transition") and gate's
		// own add-waiter idempotent notice. rc stays 0 (idempotent resolve is fine).
		if gateAlreadyResolved(issue) {
			if jsonOutput {
				return outputJSON(map[string]interface{}{
					"id":       gateID,
					"resolved": true,
					"reason":   issue.CloseReason,
				})
			}
			fmt.Printf("%s Gate %s was already resolved (no change)\n", ui.RenderInfoIcon(), gateID)
			return nil
		}

		gateOldStatus := string(issue.Status)
		if err := store.CloseIssue(ctx, gateID, reason, actor, ""); err != nil {
			return HandleErrorRespectJSON("closing gate: %v", err)
		}

		commandDidWrite.Store(true)

		// beads-1jkl5: resolving a gate closes it via store.CloseIssue, whose DB
		// EventClosed row survives only until a Dolt GC flatten. bd close/reopen/
		// defer/supersede/duplicate ALSO write a GC-survivable audit-FILE entry
		// (.beads/interactions.jsonl) via auditStatusChange (n4sn/r3m8v) — but
		// gate resolve dropped it, so after a flatten a resolved gate's close
		// vanished from the durable record while a plainly closed issue's did
		// not. Emit the same status field_change (audit-parity sibling of r3m8v
		// on the gate-resolve leg). The already-resolved no-op returned early
		// above, so reaching here always means a real open→closed transition;
		// store.CloseIssue autocommits durably, so this reflects a real change.
		auditStatusChange(gateID, gateOldStatus, "closed", actor, reason)

		// beads-346th: a linked gate is a real molecule step (mol show counts
		// it), so resolving a molecule's final-step gate must cascade-close the
		// parent exactly as bd close does (close.go:223). Manual gate resolve
		// closed via bare store.CloseIssue with NO cascade hop, orphaning the
		// molecule root open with every step done (CLOSE-PARITY-MATRIX). Fire the
		// same shared chokepoint; it self-guards (not-a-step / already-closed-root
		// no-op).
		autoCloseCompletedMolecule(ctx, store, gateID, actor, "")

		// beads-u3lt: gate resolve honored --json NOWHERE (success printed
		// plaintext, errors used bare HandleError) — emit a JSON success doc under
		// --json, and route the error paths through HandleErrorRespectJSON, so the
		// command has a parseable --json contract on every path.
		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"id":       gateID,
				"resolved": true,
				"reason":   reason,
			})
		}

		fmt.Printf("%s Gate resolved: %s\n", ui.RenderPass("✓"), gateID)
		if reason != "" {
			fmt.Printf("  Reason: %s\n", reason)
		}
		return nil
	},
}

// formatGateCheckReason sanitizes a gate-check reason for terminal display
// (beads-ce741, 7n9y sink class). For gh:pr / gh:run gates the reason embeds
// UNTRUSTED external SCM data — the GitHub PR title (checkGHPR, `gh pr view
// --json state,title`) or workflow name (checkGHRunStatus, `gh run view --json
// ...,name`) — so an attacker-controlled PR title carrying OSC/CSI escapes
// would inject terminal-control sequences (OSC 0 window-title, OSC 52 clipboard)
// into the maintainer's terminal on `bd gate check`. Display-only: the raw
// reason still flows to closeGate/escalateGate for stored/relayed fidelity.
func formatGateCheckReason(reason string) string {
	return ui.SanitizeForTerminal(reason)
}

// gateCheckCmd evaluates gates and closes those that are resolved
var gateCheckCmd = &cobra.Command{
	Use:   "check",
	Args:  cobra.NoArgs, // beads-8jy7e: reject stray positionals with a clean usage error
	Short: "Evaluate gates and close resolved ones",
	Long: `Evaluate gate conditions and automatically close resolved gates.

By default, checks all open gates. Use --type to filter by gate type.

Gate types:
  gh       - Check all GitHub gates (gh:run and gh:pr)
  gh:run   - Check GitHub Actions workflow runs
  gh:pr    - Check pull request merge status
  timer    - Check timer gates (auto-expire based on timeout)
  all      - Check all gate types

GitHub gates use the 'gh' CLI to query status:
  - gh:run checks 'gh run view <id> --json status,conclusion'
  - gh:pr checks 'gh pr view <id> --json state,title'

A gate is resolved when:
  - gh:run: status=completed AND conclusion=success
  - gh:pr: state=MERGED
  - timer: current time > created_at + timeout

A gate is escalated when:
  - gh:run: status=completed AND conclusion in (failure, canceled)
  - gh:pr: state=CLOSED

Examples:
  bd gate check              # Check all gates
  bd gate check --type=gh    # Check only GitHub gates
  bd gate check --type=gh:run # Check only workflow run gates
  bd gate check --type=timer # Check only timer gates
  bd gate check --dry-run    # Show what would happen without changes
  bd gate check --escalate   # Escalate expired/failed gates`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("gate check")

		evt := metrics.NewCommandEvent("gate-check")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		gateTypeFilter, _ := cmd.Flags().GetString("type")
		// beads-68cgv: reject an unknown/typo/retired --type filter up front.
		// shouldCheckGate uses exact-match, so a value like "ghpr" or the retired
		// "bead" would fall through, match zero gates, and print "No open gates of
		// type X found" + exit 0 — reading as "all clear" while real gates go
		// unchecked. Mirrors the ds9tr validateGateCreate fail-early guard.
		if err := validateGateCheckType(gateTypeFilter); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		escalateFlag, _ := cmd.Flags().GetBool("escalate")
		limit, _ := cmd.Flags().GetInt("limit")
		// Reject a negative --limit up front (beads-eqi4): the SQL builders
		// only apply filter.Limit when >0, so a negative value silently returns
		// the full set. Shared with bd list (uh4i) via validateLimitFromCmd.
		if err := validateLimitFromCmd(cmd); err != nil {
			return err
		}

		gateType := types.IssueType("gate")
		filter := types.IssueFilter{
			IssueType:     &gateType,
			ExcludeStatus: []types.Status{types.StatusClosed},
			Limit:         limit,
		}

		ctx := rootCtx
		var gates []*types.Issue
		var err error

		if usesProxiedServer() {
			gates, err = searchGatesProxied(ctx, filter)
		} else {
			gates, err = store.SearchIssues(ctx, "", filter)
		}
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		var filteredGates []*types.Issue
		for _, gate := range gates {
			if shouldCheckGate(gate, gateTypeFilter) {
				filteredGates = append(filteredGates, gate)
			}
		}

		if len(filteredGates) == 0 {
			// beads-u3lt: under --json the empty case must still emit the summary
			// JSON doc (zero counts) on stdout, not plaintext + bare return — the
			// bare return here previously produced plaintext + ZERO json on the
			// COMMON empty case, so `bd gate check --json | jq` failed to parse.
			if jsonOutput {
				return outputJSON(map[string]interface{}{
					"checked":   0,
					"resolved":  0,
					"escalated": 0,
					"errors":    0,
					"dry_run":   dryRun,
				})
			}
			if gateTypeFilter != "" {
				fmt.Printf("No open gates of type '%s' found.\n", gateTypeFilter)
			} else {
				fmt.Println("No open gates found.")
			}
			return nil
		}

		// Results tracking
		type checkResult struct {
			gate      *types.Issue
			resolved  bool
			escalated bool
			reason    string
			err       error
		}
		results := make([]checkResult, 0, len(filteredGates))

		// Check each gate
		now := time.Now()
		for _, gate := range filteredGates {
			result := checkResult{gate: gate}

			switch {
			case strings.HasPrefix(gate.AwaitType, "gh:run"):
				result.resolved, result.escalated, result.reason, result.err = checkGHRun(gate, !dryRun)
			case strings.HasPrefix(gate.AwaitType, "gh:pr"):
				result.resolved, result.escalated, result.reason, result.err = checkGHPR(gate)
			case gate.AwaitType == "timer":
				result.resolved, result.escalated, result.reason, result.err = checkTimer(gate, now)
			default:
				// Skip unsupported gate types (human gates need manual resolution;
				// bead gates were retired in beads-kburh — multi-rig routing is gone,
				// so cross-rig bead gates could never resolve).
				continue
			}

			results = append(results, result)
		}

		// Process results
		resolvedCount := 0
		escalatedCount := 0
		errorCount := 0

		for _, r := range results {
			if r.err != nil {
				errorCount++
				fmt.Fprintf(os.Stderr, "%s %s: error checking - %v\n",
					ui.RenderFail("✗"), r.gate.ID, r.err)
				continue
			}

			// beads-u3lt: gate the per-gate PROGRESS prints behind !jsonOutput so
			// under --json only the summary JSON doc (below) reaches stdout — these
			// human-progress lines previously printed to stdout unconditionally,
			// THEN the :673 json doc followed, yielding "human text + json" =
			// unparseable ("Extra data"). Side effects (closeGate/escalateGate) and
			// stderr error lines are unchanged; only the stdout progress text is gated.
			if r.resolved {
				resolvedCount++
				if dryRun {
					if !jsonOutput {
						fmt.Printf("%s %s: would resolve - %s\n",
							ui.RenderPass("✓"), r.gate.ID, formatGateCheckReason(r.reason))
					}
				} else {
					// Close the gate. Pass the pre-close status (r.gate is the
					// open gate fetched by the check loop) so closeGate can write
					// the GC-survivable audit-file trail at parity with manual
					// resolve (beads-8ociu / beads-1jkl5).
					closeErr := closeGate(ctx, r.gate.ID, string(r.gate.Status), r.reason)
					if closeErr != nil {
						fmt.Fprintf(os.Stderr, "%s %s: error closing - %v\n",
							ui.RenderFail("✗"), r.gate.ID, closeErr)
						errorCount++
					} else if !jsonOutput {
						fmt.Printf("%s %s: resolved - %s\n",
							ui.RenderPass("✓"), r.gate.ID, formatGateCheckReason(r.reason))
					}
				}
			} else if r.escalated {
				escalatedCount++
				if dryRun {
					if !jsonOutput {
						fmt.Printf("%s %s: would escalate - %s\n",
							ui.RenderWarn("⚠"), r.gate.ID, formatGateCheckReason(r.reason))
					}
				} else {
					if !jsonOutput {
						fmt.Printf("%s %s: ESCALATE - %s\n",
							ui.RenderWarn("⚠"), r.gate.ID, formatGateCheckReason(r.reason))
					}
					// Actually escalate if flag is set
					if escalateFlag {
						escalateGate(r.gate, r.reason)
					}
				}
			} else if !jsonOutput {
				// Still pending
				fmt.Printf("%s %s: pending - %s\n",
					ui.RenderAccent("○"), r.gate.ID, formatGateCheckReason(r.reason))
			}
		}

		// Summary (human only; under --json the summary is the JSON doc below)
		if !jsonOutput {
			fmt.Println()
			fmt.Printf("Checked %d gates: %d resolved, %d escalated, %d errors\n",
				len(results), resolvedCount, escalatedCount, errorCount)
		}

		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"checked":   len(results),
				"resolved":  resolvedCount,
				"escalated": escalatedCount,
				"errors":    errorCount,
				"dry_run":   dryRun,
			})
		}
		return nil
	},
}

// validateGateCheckType rejects an unknown/typo/retired `bd gate check --type`
// filter (beads-68cgv). Without this, shouldCheckGate's exact-match silently
// drops an unrecognized filter to zero matches — the command prints "No open
// gates of type X found" and exits 0, so a typo like "ghpr" or the retired
// "bead" reads as "all clear" while real gh:pr/timer gates go unchecked. The
// accepted set mirrors the --type flag help: the "" (all) default, the "all"
// and "gh" aggregates, and each concrete gate type.
func validateGateCheckType(typeFilter string) error {
	switch typeFilter {
	case "", "all", "gh", "gh:run", "gh:pr", "timer", "human":
		return nil
	default:
		return fmt.Errorf("invalid gate type filter %q (must be one of: gh, gh:run, gh:pr, timer, human, all)", typeFilter)
	}
}

// shouldCheckGate returns true if the gate matches the type filter
func shouldCheckGate(gate *types.Issue, typeFilter string) bool {
	if typeFilter == "" || typeFilter == "all" {
		return true
	}
	if typeFilter == "gh" {
		return strings.HasPrefix(gate.AwaitType, "gh:")
	}
	return gate.AwaitType == typeFilter
}

// ghRunStatus holds the JSON response from 'gh run view'
type ghRunStatus struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Name       string `json:"name"`
}

// ghPRStatus holds the JSON response from 'gh pr view'
type ghPRStatus struct {
	State string `json:"state"`
	Title string `json:"title"`
}

var (
	discoverRunIDByWorkflowNameFunc = discoverRunIDByWorkflowName
	updateGateAwaitIDFunc           = updateGateAwaitID
	checkGHRunStatusFunc            = checkGHRunStatus
)

// isNumericID returns true if the string contains only digits (a GitHub run ID)
func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// queryGitHubRunsForWorkflow queries recent runs for a specific workflow using gh CLI.
// Returns runs sorted newest-first (GitHub API default).
func queryGitHubRunsForWorkflow(workflow string, limit int) ([]GHWorkflowRun, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found: install from https://cli.github.com")
	}

	args := []string{
		"run", "list",
		"--workflow", workflow,
		"--json", "databaseId,name,status,conclusion,createdAt,workflowName",
		"--limit", fmt.Sprintf("%d", limit),
	}

	cmd := exec.Command("gh", args...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh run list --workflow=%s failed: %s", workflow, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("gh run list: %w", err)
	}

	var runs []GHWorkflowRun
	if err := json.Unmarshal(output, &runs); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	return runs, nil
}

// discoverRunIDByWorkflowName queries GitHub for the most recent run of a workflow.
// Returns (runID, error). This is ZFC-compliant: "most recent run" is deterministic.
func discoverRunIDByWorkflowName(workflowHint string) (string, error) {
	// Query GitHub directly for this workflow (efficient, avoids limit issues)
	runs, err := queryGitHubRunsForWorkflow(workflowHint, 5)
	if err != nil {
		return "", fmt.Errorf("failed to query workflow runs: %w", err)
	}

	if len(runs) == 0 {
		return "", fmt.Errorf("no runs found for workflow '%s'", workflowHint)
	}

	// Take the most recent run (gh returns newest-first)
	// This is deterministic: "most recent" is a total ordering by creation time
	return fmt.Sprintf("%d", runs[0].DatabaseID), nil
}

// checkGHRun checks a GitHub Actions workflow run gate.
// When persistDiscoveredRunID is false, workflow-name discovery stays in-memory only.
func checkGHRun(gate *types.Issue, persistDiscoveredRunID bool) (resolved, escalated bool, reason string, err error) {
	if gate.AwaitID == "" {
		return false, false, "no run ID specified - set await_id or use workflow name hint", nil
	}

	runID := gate.AwaitID

	// If await_id is a workflow name hint (non-numeric), auto-discover the run ID
	if !isNumericID(gate.AwaitID) {
		discoveredID, discoverErr := discoverRunIDByWorkflowNameFunc(gate.AwaitID)
		if discoverErr != nil {
			return false, false, fmt.Sprintf("workflow hint '%s': %v", gate.AwaitID, discoverErr), nil
		}

		if persistDiscoveredRunID {
			// Non-dry-run flows persist the numeric run ID for future checks.
			if updateErr := updateGateAwaitIDFunc(nil, gate.ID, discoveredID); updateErr != nil {
				return false, false, "", fmt.Errorf("failed to update gate with discovered run ID: %w", updateErr)
			}
		}

		runID = discoveredID
	}

	return checkGHRunStatusFunc(runID)
}

func checkGHRunStatus(runID string) (resolved, escalated bool, reason string, err error) {
	// Run: gh run view <id> --json status,conclusion,name
	cmd := exec.Command("gh", "run", "view", runID, "--json", "status,conclusion,name") // #nosec G204 -- runID is a validated GitHub run ID
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		// Check if gh CLI is not found
		if strings.Contains(stderr.String(), "command not found") ||
			strings.Contains(runErr.Error(), "executable file not found") {
			return false, false, "", fmt.Errorf("gh CLI not installed")
		}
		// Check if run not found
		if strings.Contains(stderr.String(), "not found") {
			return false, true, "workflow run not found", nil
		}
		return false, false, "", fmt.Errorf("gh run view failed: %s", stderr.String())
	}

	var status ghRunStatus
	if parseErr := json.Unmarshal(stdout.Bytes(), &status); parseErr != nil {
		return false, false, "", fmt.Errorf("failed to parse gh output: %w", parseErr)
	}

	// Evaluate status
	switch status.Status {
	case "completed":
		switch status.Conclusion {
		case "success":
			return true, false, fmt.Sprintf("workflow '%s' succeeded", status.Name), nil
		case "failure":
			return false, true, fmt.Sprintf("workflow '%s' failed", status.Name), nil
		case "cancelled", "canceled":
			return false, true, fmt.Sprintf("workflow '%s' was canceled", status.Name), nil
		case "skipped":
			return true, false, fmt.Sprintf("workflow '%s' was skipped", status.Name), nil
		default:
			return false, true, fmt.Sprintf("workflow '%s' concluded with %s", status.Name, status.Conclusion), nil
		}
	case "in_progress", "queued", "pending", "waiting":
		return false, false, fmt.Sprintf("workflow '%s' is %s", status.Name, status.Status), nil
	default:
		return false, false, fmt.Sprintf("workflow '%s' status: %s", status.Name, status.Status), nil
	}
}

// checkGHPR checks a GitHub pull request gate
func checkGHPR(gate *types.Issue) (resolved, escalated bool, reason string, err error) {
	if gate.AwaitID == "" {
		return false, false, "no PR number specified", nil
	}

	// Run: gh pr view <id> --json state,title
	cmd := exec.Command("gh", "pr", "view", gate.AwaitID, "--json", "state,title") // #nosec G204 -- gate.AwaitID is a validated GitHub PR number
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		// Check if gh CLI is not found
		if strings.Contains(stderr.String(), "command not found") ||
			strings.Contains(runErr.Error(), "executable file not found") {
			return false, false, "", fmt.Errorf("gh CLI not installed")
		}
		// Check if PR not found
		if strings.Contains(stderr.String(), "not found") || strings.Contains(stderr.String(), "Could not resolve") {
			return false, true, "pull request not found", nil
		}
		return false, false, "", fmt.Errorf("gh pr view failed: %s", stderr.String())
	}

	var status ghPRStatus
	if parseErr := json.Unmarshal(stdout.Bytes(), &status); parseErr != nil {
		return false, false, "", fmt.Errorf("failed to parse gh output: %w", parseErr)
	}

	// Evaluate status
	switch status.State {
	case "MERGED":
		return true, false, fmt.Sprintf("PR '%s' was merged", status.Title), nil
	case "CLOSED":
		return false, true, fmt.Sprintf("PR '%s' was closed without merging", status.Title), nil
	case "OPEN":
		return false, false, fmt.Sprintf("PR '%s' is still open", status.Title), nil
	default:
		return false, false, fmt.Sprintf("PR '%s' state: %s", status.Title, status.State), nil
	}
}

// checkTimer checks a timer gate for expiration
// Note: timers resolve but never escalate (escalated is always false by design)
func checkTimer(gate *types.Issue, now time.Time) (resolved, escalated bool, reason string, err error) { //nolint:unparam // escalated intentionally always false
	if gate.Timeout == 0 {
		return false, false, "timer gate without timeout configured", fmt.Errorf("no timeout set")
	}

	expiresAt := gate.CreatedAt.Add(gate.Timeout)
	if now.After(expiresAt) {
		expired := now.Sub(expiresAt).Round(time.Second)
		return true, false, fmt.Sprintf("timer expired %s ago", expired), nil
	}

	remaining := expiresAt.Sub(now).Round(time.Second)
	return false, false, fmt.Sprintf("expires in %s", remaining), nil
}

// closeGate closes a gate issue with the given reason
func closeGate(_ interface{}, gateID, oldStatus, reason string) error {
	if usesProxiedServer() {
		if err := closeGateProxied(gateID, oldStatus, reason); err != nil {
			return err
		}
		commandDidWrite.Store(true)
		return nil
	}
	if err := store.CloseIssue(rootCtx, gateID, reason, actor, ""); err != nil {
		return err
	}
	commandDidWrite.Store(true)
	// beads-8ociu: the gate check AUTO-RESOLVE path (timer/gh:run/gh:pr) closes
	// the gate via store.CloseIssue but dropped the GC-survivable audit-FILE
	// trail (.beads/interactions.jsonl) that manual `bd gate resolve` writes via
	// auditStatusChange (beads-1jkl5) and that bd close/supersede emit (n4sn/
	// r3m8v). So an auto-resolved gate's close vanished from the durable record
	// after a Dolt GC flatten while a manually-resolved one's did not. Emit the
	// same status field_change (auto-resolve sibling of 1jkl5). The check loop
	// only reaches closeGate for a resolved OPEN gate, so this is a real
	// open→closed transition; store.CloseIssue autocommits durably.
	auditStatusChange(gateID, oldStatus, "closed", actor, reason)
	// beads-346th: mol show counts a linked gate as a real molecule step, so
	// closing a gate that is a molecule's final step must cascade-close the
	// parent exactly as bd close does (close.go:223). The gate check auto-close
	// loop closed via store.CloseIssue with NO cascade hop, so a molecule whose
	// last step is a timer/PR/run gate silently stayed open with every step done
	// (CLOSE-PARITY-MATRIX, sibling of 4v7eb's epic close-eligible leg). Fire the
	// same shared chokepoint bd close/todo done/duplicate use; it self-guards
	// (not-a-molecule-step and already-closed-root both no-op).
	autoCloseCompletedMolecule(rootCtx, store, gateID, actor, "")
	return nil
}

// escalateGate sends an escalation for a failed/expired gate
func escalateGate(gate *types.Issue, reason string) {
	topic := fmt.Sprintf("Gate escalation: %s", gate.ID)
	message := fmt.Sprintf("Gate %s needs attention.\nType: %s\nReason: %s\nCreated: %s",
		gate.ID,
		gate.AwaitType,
		reason,
		gate.CreatedAt.Format(time.RFC3339))

	// Call gt escalate if available
	escalateCmd := exec.Command("gt", "escalate", topic, "-s", "HIGH", "-m", message)
	escalateCmd.Stdout = os.Stdout
	escalateCmd.Stderr = os.Stderr
	if err := escalateCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: escalation failed for %s: %v\n", gate.ID, err)
	}
}

func init() {
	// gate list flags
	gateListCmd.Flags().BoolP("all", "a", false, "Show all gates including closed")
	gateListCmd.Flags().IntP("limit", "n", 50, "Limit results (default 50)")

	// gate resolve flags
	gateResolveCmd.Flags().StringP("reason", "r", "", "Reason for resolving the gate")

	// gate check flags
	gateCheckCmd.Flags().StringP("type", "t", "", "Gate type to check (gh, gh:run, gh:pr, timer, human, all)")
	gateCheckCmd.Flags().Bool("dry-run", false, "Show what would happen without making changes")
	gateCheckCmd.Flags().BoolP("escalate", "e", false, "Escalate failed/expired gates")
	gateCheckCmd.Flags().IntP("limit", "l", 100, "Limit results (default 100)")

	// gate create flags
	gateCreateCmd.Flags().String("blocks", "", "Issue ID to block (required)")
	gateCreateCmd.Flags().StringP("type", "t", "human", "Gate type (human, timer, gh:run, gh:pr)")
	gateCreateCmd.Flags().StringP("reason", "r", "", "Reason for the gate")
	gateCreateCmd.Flags().String("await-id", "", "Condition identifier (run ID, PR number, etc.)")
	gateCreateCmd.Flags().String("timeout", "", "Timeout duration (e.g., 2h, 30m)")
	_ = gateCreateCmd.MarkFlagRequired("blocks")

	// Issue ID completions
	gateShowCmd.ValidArgsFunction = issueIDCompletion
	gateResolveCmd.ValidArgsFunction = issueIDCompletion
	gateAddWaiterCmd.ValidArgsFunction = issueIDCompletion
	gateCreateCmd.ValidArgsFunction = issueIDCompletion

	// Add subcommands
	gateCmd.AddCommand(gateListCmd)
	gateCmd.AddCommand(gateCreateCmd)
	gateCmd.AddCommand(gateShowCmd)
	gateCmd.AddCommand(gateResolveCmd)
	gateCmd.AddCommand(gateCheckCmd)
	gateCmd.AddCommand(gateAddWaiterCmd)

	rootCmd.AddCommand(gateCmd)
}

// validateGateCreate enforces the gate-type invariants at create time so
// `bd gate create` cannot mint a gate that `bd gate check` can never resolve
// (beads-ds9tr). It validates the documented type set (human|timer|gh:run|gh:pr)
// and that a timer gate carries a --timeout, mirroring the create-time timeout
// FORMAT check that already exists. Without this, an unknown --type is silently
// skipped by gate check (default: continue) and a timer without --timeout errors
// on every check ("no timeout set") — either way the blocked issue is stranded
// out of bd ready forever, with manual close the only escape.
//
// beads-9jtzh (uncovered ds9tr leg): a gh:pr gate requires --await-id (the PR
// number) at create. Unlike gh:run — which self-rescues via `bd gate discover`
// populating await_id post-create (needsDiscovery is gated to gh:run only) —
// gh:pr has NO discover path and NO post-create await_id setter, so checkGHPR
// on an empty AwaitID returns "no PR number specified" (pending, never resolves,
// never escalates) → the blocked issue is stranded forever. Requiring await_id
// up front is the ds9tr-consistent create-time guard.
//
// beads-cx0eu (uncovered ds9tr leg): a timer --timeout must be POSITIVE, not
// merely non-empty and well-formed. time.ParseDuration accepts "0s" and negative
// durations, so the string-non-empty check let two degenerate timers through:
// --timeout=0s persists Timeout==0, which checkTimer treats identically to a
// missing timeout ("no timeout set" error on every check → stranded forever, the
// exact ds9tr strand); a negative --timeout puts expiresAt in the past so the
// "timer" resolves on the very first check without ever waiting (silently
// degenerate — not the wait the caller asked for). Validate the parsed duration,
// not the raw string.
func validateGateCreate(gateType, awaitID, timeoutStr string) error {
	switch gateType {
	case "human", "gh:run":
		// resolvable types; no timeout required
	case "gh:pr":
		if awaitID == "" {
			return fmt.Errorf("gate type \"gh:pr\" requires --await-id (a PR number); a gh:pr gate has no auto-discovery, so without a PR number it can never resolve and would block the issue forever")
		}
	case "timer":
		if timeoutStr == "" {
			return fmt.Errorf("gate type \"timer\" requires --timeout (an infinite timer can never resolve and would block the issue forever)")
		}
		// The call sites already reject a malformed --timeout; a well-formed but
		// non-positive duration (0s / negative) is still unresolvable, so guard
		// it here. Ignore a parse error — the format check upstream owns that.
		if d, perr := time.ParseDuration(timeoutStr); perr == nil && d <= 0 {
			return fmt.Errorf("gate type \"timer\" requires a positive --timeout (got %q); a zero timer errors on every check (\"no timeout set\") and a negative timer expires before it starts, so neither can hold the issue as intended", timeoutStr)
		}
	default:
		return fmt.Errorf("invalid gate type %q (must be one of: human, timer, gh:run, gh:pr)", gateType)
	}
	return nil
}
