package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/timeparsing"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/steveyegge/beads/internal/validation"
)

var updateCmd = &cobra.Command{
	Use:     "update [id...]",
	GroupID: "issues",
	Short:   "Update one or more issues",
	Long: `Update one or more issues.

If no issue ID is provided, updates the last touched issue (from most recent
create, update, show, or close operation).`,
	Args:          cobra.MinimumNArgs(0),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("update")

		evt := metrics.NewCommandEvent("update")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if usesProxiedServer() {
			runUpdateProxiedServer(cmd, rootCtx, args)
			return nil
		}

		// If no IDs provided, use last touched issue
		if len(args) == 0 {
			lastTouched := GetLastTouchedID()
			if lastTouched == "" {
				return HandleErrorRespectJSON("no issue ID provided and no last touched issue")
			}
			args = []string{lastTouched}
		}

		updates := make(map[string]interface{})
		// clearDeferStatus: set per-issue in the update loop when --defer=""
		// was given without an explicit --status, to flip status=deferred back
		// to open (matches the help text's "show in bd ready immediately").
		var clearDeferStatus bool

		if cmd.Flags().Changed("status") {
			status, _ := cmd.Flags().GetString("status")
			// Case-fold built-in statuses (OPEN->open, In_Progress->in_progress)
			// so the write path accepts the same case-variants the read/filter
			// path does (beads-gqvu, write sibling of beads-7wrj). Custom
			// statuses stay case-sensitive: Status.Normalize only folds when the
			// lowercased form is a built-in.
			status = string(types.Status(status).Normalize())
			var customStatuses []string
			if store != nil {
				cs, err := store.GetCustomStatuses(rootCtx)
				if err != nil {
					if !jsonOutput {
						fmt.Fprintf(os.Stderr, "%s Failed to get custom statuses: %v\n", ui.RenderWarn("!"), err)
					}
				} else {
					customStatuses = cs
				}
			}
			if !types.Status(status).IsValidWithCustom(customStatuses) {
				return HandleErrorRespectJSON("invalid status %q (built-in: open, in_progress, blocked, deferred, closed, pinned, hooked; or configure custom statuses via 'bd config set status.custom')", status)
			}
			updates["status"] = status

			// If status is being set to closed, include session if provided
			if status == "closed" {
				session, _ := cmd.Flags().GetString("session")
				if session == "" {
					session = os.Getenv("CLAUDE_SESSION_ID")
				}
				if session != "" {
					updates["closed_by_session"] = session
				}
			}
		}
		if cmd.Flags().Changed("priority") {
			priorityStr, _ := cmd.Flags().GetString("priority")
			priority, err := validation.ValidatePriority(priorityStr)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			updates["priority"] = priority
		}
		if cmd.Flags().Changed("title") {
			title, _ := cmd.Flags().GetString("title")
			title = strings.TrimSpace(title)
			if title == "" {
				return HandleErrorRespectJSON("title cannot be empty")
			}
			updates["title"] = title
		}
		if cmd.Flags().Changed("assignee") {
			assignee, _ := cmd.Flags().GetString("assignee")
			// Trim + fold "none" through the shared normalizer so `bd update
			// --assignee "  x  "` stores the canonical form the read/filter side
			// matches (beads-llzt). Otherwise the padded value is unmatchable by
			// `bd ready/list --assignee x`, orphaning the work.
			updates["assignee"] = normalizeAssignee(assignee)
		}
		description, descChanged, err := getDescriptionFlag(cmd)
		if err != nil {
			return err
		}
		if descChanged {
			if err := validateDescriptionUpdate(cmd, description, descChanged); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			updates["description"] = description
		}
		design, designChanged, err := getDesignFlag(cmd)
		if err != nil {
			return err
		}
		if designChanged {
			updates["design"] = design
		}
		if cmd.Flags().Changed("notes") && cmd.Flags().Changed("append-notes") {
			return HandleErrorRespectJSON("cannot specify both --notes and --append-notes")
		}
		if cmd.Flags().Changed("notes") {
			notes, _ := cmd.Flags().GetString("notes")
			updates["notes"] = notes
		}
		if cmd.Flags().Changed("append-notes") {
			appendNotes, _ := cmd.Flags().GetString("append-notes")
			updates["append_notes"] = appendNotes
		}
		if cmd.Flags().Changed("acceptance") || cmd.Flags().Changed("acceptance-criteria") {
			var acceptanceCriteria string
			if cmd.Flags().Changed("acceptance") {
				acceptanceCriteria, _ = cmd.Flags().GetString("acceptance")
			} else {
				acceptanceCriteria, _ = cmd.Flags().GetString("acceptance-criteria")
			}
			updates["acceptance_criteria"] = acceptanceCriteria
		}
		if cmd.Flags().Changed("external-ref") {
			externalRef, _ := cmd.Flags().GetString("external-ref")
			// Empty string clears the ref to SQL NULL, mirroring buildCreateIssue's
			// nil-when-empty pointer semantics so cleared refs round-trip as a
			// missing field (omitempty) instead of an empty string. GH#3902.
			if externalRef == "" {
				updates["external_ref"] = nil
			} else {
				updates["external_ref"] = externalRef
			}
		}
		if cmd.Flags().Changed("spec-id") {
			specID, _ := cmd.Flags().GetString("spec-id")
			updates["spec_id"] = specID
		}
		if cmd.Flags().Changed("estimate") {
			estimate, _ := cmd.Flags().GetInt("estimate")
			if estimate < 0 {
				return HandleErrorRespectJSON("estimate must be a non-negative number of minutes")
			}
			updates["estimated_minutes"] = estimate
		}
		if cmd.Flags().Changed("type") {
			issueType, _ := cmd.Flags().GetString("type")
			// Normalize aliases (e.g., "enhancement" -> "feature") before validating.
			// Type validation (including custom types) is handled by the storage
			// layer inside the transaction, matching the create path. (GH#3030)
			issueType = utils.NormalizeIssueType(issueType)
			updates["issue_type"] = issueType
		}
		if cmd.Flags().Changed("add-label") {
			addLabels, _ := cmd.Flags().GetStringSlice("add-label")
			updates["add_labels"] = addLabels
		}
		if cmd.Flags().Changed("remove-label") {
			removeLabels, _ := cmd.Flags().GetStringSlice("remove-label")
			updates["remove_labels"] = removeLabels
		}
		if cmd.Flags().Changed("set-labels") {
			setLabels, _ := cmd.Flags().GetStringSlice("set-labels")
			updates["set_labels"] = setLabels
		}
		if cmd.Flags().Changed("parent") {
			parent, _ := cmd.Flags().GetString("parent")
			updates["parent"] = parent
		}
		// Gate fields (bd-z6kw)
		if cmd.Flags().Changed("await-id") {
			awaitID, _ := cmd.Flags().GetString("await-id")
			updates["await_id"] = awaitID
		}
		// Time-based scheduling flags (GH#820)
		if cmd.Flags().Changed("due") {
			dueStr, _ := cmd.Flags().GetString("due")
			if dueStr == "" {
				// Empty string clears the due date
				updates["due_at"] = nil
			} else {
				t, err := timeparsing.ParseRelativeTime(dueStr, time.Now())
				if err != nil {
					return HandleErrorRespectJSON("invalid --due format %q. Examples: +6h, tomorrow, next monday, 2025-01-15", dueStr)
				}
				updates["due_at"] = t
			}
		}
		if cmd.Flags().Changed("defer") {
			deferStr, _ := cmd.Flags().GetString("defer")
			if deferStr == "" {
				// Empty string clears the defer_until and restores ready-work
				// visibility (GH#3233). Explicit --status still wins.
				updates["defer_until"] = nil
				if _, ok := updates["status"]; !ok {
					clearDeferStatus = true
				}
			} else {
				t, err := timeparsing.ParseRelativeTime(deferStr, time.Now())
				if err != nil {
					return HandleErrorRespectJSON("invalid --defer format %q. Examples: +1h, tomorrow, next monday, 2025-01-15", deferStr)
				}
				// Warn if defer date is in the past (user probably meant future)
				inPast := t.Before(time.Now())
				if inPast && !jsonOutput {
					fmt.Fprintf(os.Stderr, "%s Defer date %q is in the past. Issue will appear in bd ready immediately.\n",
						ui.RenderWarn("!"), t.Format("2006-01-02 15:04"))
					fmt.Fprintf(os.Stderr, "  Did you mean a future date? Use --defer=+1h or --defer=tomorrow\n")
				}
				updates["defer_until"] = t
				// Align with `bd defer`: set status=deferred so the ❄ icon
				// shows and the issue leaves the ready queue (GH#3233).
				// Skip for past dates so the "appears in bd ready immediately"
				// warning stays truthful, and skip if --status was set explicitly.
				if _, ok := updates["status"]; !ok && !inPast {
					updates["status"] = string(types.StatusDeferred)
				}
			}
		}
		// Ephemeral/persistent flags
		// Note: storage layer uses "wisp" field name, maps to "ephemeral" column
		ephemeralChanged := cmd.Flags().Changed("ephemeral")
		persistentChanged := cmd.Flags().Changed("persistent")
		noHistoryChanged := cmd.Flags().Changed("no-history")
		historyChanged := cmd.Flags().Changed("history")
		if ephemeralChanged && persistentChanged {
			return HandleErrorRespectJSON("cannot specify both --ephemeral and --persistent flags")
		}
		if noHistoryChanged && ephemeralChanged {
			return HandleErrorRespectJSON("cannot specify both --no-history and --ephemeral flags")
		}
		if noHistoryChanged && historyChanged {
			return HandleErrorRespectJSON("cannot specify both --no-history and --history flags")
		}
		if ephemeralChanged {
			updates["wisp"] = true
		}
		if persistentChanged {
			updates["wisp"] = false
		}
		if noHistoryChanged {
			updates["no_history"] = true
		}
		if historyChanged {
			updates["no_history"] = false
		}
		// Pinned flag (beads-9ynk): --pinned/--no-pinned set the Issue.Pinned
		// context-marker bool (the prune/purge "skips pinned" protection), which
		// the storage allowed-fields map already accepts. This is distinct from
		// status="pinned"; it does not change the lifecycle status.
		pinnedChanged := cmd.Flags().Changed("pinned")
		noPinnedChanged := cmd.Flags().Changed("no-pinned")
		if pinnedChanged && noPinnedChanged {
			return HandleErrorRespectJSON("cannot specify both --pinned and --no-pinned flags")
		}
		if pinnedChanged {
			updates["pinned"] = true
		}
		if noPinnedChanged {
			updates["pinned"] = false
		}
		// Metadata flag (GH#1413)
		if cmd.Flags().Changed("metadata") {
			metadataValue, _ := cmd.Flags().GetString("metadata")
			var metadataJSON string
			if strings.HasPrefix(metadataValue, "@") {
				// Read JSON from file
				filePath := metadataValue[1:]
				// #nosec G304 -- user explicitly provides file path via @file.json syntax
				data, err := os.ReadFile(filePath)
				if err != nil {
					return HandleErrorRespectJSON("failed to read metadata file %s: %v", filePath, err)
				}
				metadataJSON = string(data)
			} else {
				metadataJSON = metadataValue
			}
			// Validate JSON is a top-level object. This is the live update
			// RunE path; gatherUpdateInput's gate covers a separate path
			// (beads-eum2/ef2k).
			if !json.Valid([]byte(metadataJSON)) {
				return HandleErrorRespectJSON("invalid JSON in --metadata: must be valid JSON")
			}
			if !metadataIsJSONObject(metadataJSON) {
				return HandleErrorRespectJSON(`--metadata must be a JSON object, e.g. {"key":"value"} (arrays and scalars can't be edited by --set-metadata/--unset-metadata)`)
			}
			updates["metadata"] = json.RawMessage(metadataJSON)
		}

		// Incremental metadata edits (GH#1406)
		setMetadataFlags, _ := cmd.Flags().GetStringArray("set-metadata")
		unsetMetadataFlags, _ := cmd.Flags().GetStringArray("unset-metadata")
		if (len(setMetadataFlags) > 0 || len(unsetMetadataFlags) > 0) && cmd.Flags().Changed("metadata") {
			return HandleErrorRespectJSON("cannot combine --metadata with --set-metadata or --unset-metadata")
		}
		if len(setMetadataFlags) > 0 || len(unsetMetadataFlags) > 0 {
			updates["_set_metadata"] = setMetadataFlags
			updates["_unset_metadata"] = unsetMetadataFlags
		}

		// Get claim flag
		claimFlag, _ := cmd.Flags().GetBool("claim")
		// --force overrides the close-time integrity guards (blocked-by-open,
		// epic-with-open-children) when `update --status closed` reaches the
		// same terminal state `bd close` does. Mirrors `bd close --force`
		// (beads-zgku): both CLI verbs must enforce, and be able to override,
		// the same close invariants.
		forceFlag, _ := cmd.Flags().GetBool("force")

		if len(updates) == 0 && !claimFlag {
			// beads-b0lq: a valid id with no mutating field flags is an
			// idempotent no-op SUCCESS, not an error. Under --json emit a
			// parseable no-op status object (matching the migrate.go/rules.go
			// {status:"noop"} convention and the hxc2 no-op-success precedent)
			// so a machine consumer parsing stdout as JSON does not hit a parse
			// failure on this rc=0 path; the plain path keeps the human line.
			if jsonOutput {
				return outputJSON(map[string]string{
					"status":  "noop",
					"message": "no updates specified",
				})
			}
			fmt.Println("No updates specified")
			return nil
		}

		// beads-bdy2: honest "no change" on a scalar-only no-op update.
		//
		// `bd update <id> --status open` on an already-open issue (likewise
		// --priority/--title/--assignee to the current value) printed
		// "✓ Updated issue" rc=0 though nothing changed — a false-success a
		// CI/agent gate reads as proof of a change (the xqsy/dr3/b0tw no-op
		// class, here on the general update path). Distinct from b0lq above,
		// which is the no-FLAGS-at-all path; this is the flag-set-but-same-value
		// path.
		//
		// This is a DISPLAY-ONLY fix per the beads-bdy2 ruling: the UpdateIssue
		// call, audit trail, and mutation tracking are left UNCHANGED (the write
		// stays idempotent), so it cannot swallow a legitimate append-notes/
		// audit/parent-change. onlyScalarFlags is the guard — it is true ONLY
		// when every set flag maps to a scalar field (status/priority/title/
		// assignee) with no non-scalar/audit-bearing flag (notes, labels,
		// parent, metadata, description, etc.) and no --claim. Anything else
		// legitimately mutates/audits even when scalars match, so it always
		// reports "✓ Updated".
		scalarUpdateKeys := map[string]bool{
			"status":   true,
			"priority": true,
			"title":    true,
			"assignee": true,
		}
		onlyScalarFlags := len(updates) > 0 && !claimFlag
		for k := range updates {
			if !scalarUpdateKeys[k] {
				onlyScalarFlags = false
				break
			}
		}

		ctx := rootCtx

		// beads-1d32: --append-notes is NON-IDEMPOTENT, so a best-effort partial
		// apply across a mixed valid/invalid batch is a real correctness hazard —
		// the good ids get the note, the batch exits non-zero, and the natural
		// retry double-appends. bd close/delete pre-resolve every id and bail
		// before any write; do the same here, but ONLY when --append-notes is in
		// play (idempotent flags like --priority/--add-label keep the 4i20
		// best-effort partial-apply). A single-id batch cannot half-apply, so the
		// guard only matters for 2+ ids. If any id fails to resolve, error before
		// the mutation loop so no note is written and the retry appends once.
		if _, appending := updates["append_notes"]; appending && len(args) > 1 {
			for _, id := range args {
				pre, err := resolveAndGetIssueForMutation(ctx, store, id)
				if pre != nil {
					pre.Close()
				}
				if err != nil {
					return HandleErrorRespectJSON("Error resolving %s: %v", id, err)
				}
				if pre == nil || pre.Issue == nil {
					return HandleErrorRespectJSON("Issue %s not found", id)
				}
			}
		}

		updatedIssues := []*types.Issue{}
		var firstUpdatedID string // Track first successful update for last-touched
		// Count items that completed the loop body without hitting a per-item
		// failure `continue`. Every reportItemError path continues before the
		// end-of-body success point, so processedCount < len(args) means at
		// least one id failed — used to make the batch exit code honest
		// (rc!=0 when any id failed), matching `bd close`/`bd delete` instead of
		// the old "rc=0 if ANY id succeeded" which silently masked a bad id in a
		// multi-id update (beads-4i20).
		processedCount := 0
		mutatedStores := map[storage.DoltStorage][]string{}
		mutatedResults := map[*RoutedResult]bool{}
		pendingCloseResults := []*RoutedResult{}
		trackMutation := func(result *RoutedResult) {
			if result == nil || result.Store == nil {
				return
			}
			if !mutatedResults[result] {
				pendingCloseResults = append(pendingCloseResults, result)
				mutatedResults[result] = true
			}
			mutatedStores[result.Store] = append(mutatedStores[result.Store], result.ResolvedID)
		}
		closeIfUnmutated := func(result *RoutedResult) {
			if result == nil {
				return
			}
			if mutatedResults[result] {
				return
			}
			result.Close()
		}
		closePendingResults := func() {
			for _, result := range pendingCloseResults {
				result.Close()
			}
			pendingCloseResults = nil
		}
		// Per-item errors: under --json, reportItemError writes a JSON object to
		// stderr (beads-fg6, for PARTIAL success where stdout carries the updated
		// array). When EVERY id fails, the terminal path also writes a JSON error
		// object to stdout, so a `2>&1` consumer got TWO objects (beads-92tz). So
		// under --json we DEFER per-item errors and flush them to stderr only if
		// at least one issue updated (partial success); when nothing updates, the
		// single terminal stdout object is the sole error and stderr stays clean.
		var deferredItemErrors []string
		reportUpdateItemError := func(format string, a ...interface{}) {
			if jsonOutput {
				deferredItemErrors = append(deferredItemErrors, fmt.Sprintf(format, a...))
				return
			}
			reportItemError(format, a...)
		}
		for _, id := range args {
			// Resolve and get issue with routing (e.g., gt-xyz routes to another rig)
			result, err := resolveAndGetIssueForMutation(ctx, store, id)
			if err != nil {
				if result != nil {
					result.Close()
				}
				reportUpdateItemError("Error resolving %s: %v", id, err)
				continue
			}
			if result == nil || result.Issue == nil {
				if result != nil {
					result.Close()
				}
				reportUpdateItemError("Issue %s not found", id)
				continue
			}
			issue := result.Issue
			issueStore := result.Store

			if err := validateIssueUpdatable(id, issue); err != nil {
				reportUpdateItemError("%s", err)
				closeIfUnmutated(result)
				continue
			}

			// Close-integrity guards (beads-zgku): `update --status closed`
			// reaches the same terminal state as `bd close`, so it must enforce
			// the same close-time invariants that live at the CLI layer in
			// close.go — the epic-with-open-children guard (close.go:145) and
			// the blocked-by-open-issues guard (close.go:166). Without this,
			// `bd update --status closed` silently bypasses both. Only runs on a
			// real open->closed transition (already-closed is a no-op close) and
			// is overridable with --force, matching `bd close --force`.
			if newStatus, ok := updates["status"].(string); ok && newStatus == "closed" &&
				!forceFlag && issue.Status != types.StatusClosed {
				// Epic close guard: prevent closing epics with open children.
				if issue.IssueType == types.TypeEpic {
					if openChildren := countEpicOpenChildren(ctx, issueStore, result.ResolvedID); openChildren > 0 {
						reportItemError("cannot close epic %s: %d open child issue(s); close children first or use --force to override", id, openChildren)
						closeIfUnmutated(result)
						continue
					}
				}
				// Blocked close guard: prevent closing an issue with open blockers.
				blocked, blockers, err := issueStore.IsBlocked(ctx, result.ResolvedID)
				if err != nil {
					reportItemError("Error checking blockers for %s: %v", id, err)
					closeIfUnmutated(result)
					continue
				}
				if blocked && len(blockers) > 0 {
					reportItemError("cannot close %s: blocked by open issues %v (use --force to override)", id, blockers)
					closeIfUnmutated(result)
					continue
				}
			}

			// Epic-demote close-guard bypass (beads-2hkd): the epic-with-open-
			// children close guard (close.go:145 / the zgku guard above) keys on
			// issue_type==epic AT CLOSE TIME. `bd update --type task` demotes the
			// epic first, so a subsequent close no longer sees it as an epic and
			// succeeds with children still open (no --force) — reaching a
			// closed-epic-with-open-child state via a different mutation path than
			// the alternate close verbs that zgku/1d08 hardened. Enforce the same
			// invariant on the demote transition: refuse to demote an epic that
			// still has open children (epic -> non-epic), overridable with --force
			// to match `bd close --force`. Only guards a real epic->non-epic
			// transition; epic->epic re-normalization and promotions are unaffected.
			if newTypeRaw, ok := updates["issue_type"].(string); ok && !forceFlag &&
				issue.IssueType == types.TypeEpic && types.IssueType(newTypeRaw).Normalize() != types.TypeEpic {
				if openChildren := countEpicOpenChildren(ctx, issueStore, result.ResolvedID); openChildren > 0 {
					reportItemError("cannot demote epic %s to %s: %d open child issue(s); close children first or use --force to override", id, newTypeRaw, openChildren)
					closeIfUnmutated(result)
					continue
				}
			}

			// Child-reopen close-guard bypass (beads-b0tw): reopening a closed
			// child whose parent epic is itself closed (via `update --status open`)
			// silently recreates the closed-epic-with-open-child state — the same
			// invariant the close-guard family enforces, bypassed at the child
			// status->open transition rather than at epic close. Mirrors the
			// `bd reopen` guard. Only a real closed->open transition triggers it
			// (issue was closed, new status is open) and it is overridable with
			// --force, matching `bd close --force`.
			if newStatus, ok := updates["status"].(string); ok && !forceFlag &&
				types.Status(newStatus) == types.StatusOpen && issue.Status == types.StatusClosed {
				if closedEpics := closedEpicParents(ctx, issueStore, result.ResolvedID); len(closedEpics) > 0 {
					reportItemError("cannot reopen %s: its parent epic %v is closed; reopen the epic first or use --force to override", id, closedEpics)
					closeIfUnmutated(result)
					continue
				}
			}

			// Handle claim operation atomically using compare-and-swap semantics
			if claimFlag {
				if err := issueStore.ClaimIssue(ctx, result.ResolvedID, actor); err != nil {
					reportUpdateItemError("Error claiming %s: %v", id, err)
					closeIfUnmutated(result)
					continue
				}
				trackMutation(result)
			}

			// Apply regular field updates if any
			regularUpdates := make(map[string]interface{})
			for k, v := range updates {
				if k != "add_labels" && k != "remove_labels" && k != "set_labels" && k != "parent" && k != "append_notes" &&
					k != "_set_metadata" && k != "_unset_metadata" {
					regularUpdates[k] = v
				}
			}
			// GH#3233: --defer="" restores ready visibility only if the issue
			// was actually deferred. Other statuses (blocked, in_progress, …)
			// shouldn't be clobbered just because defer_until was stale.
			if clearDeferStatus && issue.Status == types.StatusDeferred {
				regularUpdates["status"] = string(types.StatusOpen)
			}

			// --metadata is a whole-blob MERGE with arbitrary (unvalidated) keys,
			// so it can't safely decompose into per-key JSON_SET paths. Guard it
			// against concurrent clobber with an optimistic CAS merge done wholly
			// server-side (issueStore.MergeMetadataWithCAS: re-read + re-merge +
			// bounded retry on updated_at conflict), instead of the old client-
			// side read-modify-write that lost concurrent edits (beads-fnp6). A
			// merge failure is a per-item error (reportUpdateItemError + continue,
			// never `return` out of the batch — that would poison the --json
			// stdout contract; beads-92tz defers the object so an all-failed batch
			// emits exactly one error object).
			if newMeta, ok := regularUpdates["metadata"].(json.RawMessage); ok {
				delete(regularUpdates, "metadata") // applied via CAS below, not whole-blob
				if err := issueStore.MergeMetadataWithCAS(ctx, result.ResolvedID, newMeta, actor); err != nil {
					reportUpdateItemError("metadata merge failed for %s: %v", id, err)
					closeIfUnmutated(result)
					continue
				}
				trackMutation(result)
			}
			// --set/--unset-metadata are per-KEY edits (validated keys), applied
			// server-side atomically (JSON_SET / JSON_REMOVE) below so concurrent
			// per-key edits to the same issue don't clobber each other. Parsed
			// here (same per-item error contract).
			var metaSets map[string]json.RawMessage
			var metaUnsets []string
			if setMeta, ok := updates["_set_metadata"].([]string); ok {
				unsetMeta, _ := updates["_unset_metadata"].([]string)
				s, u, err := parseMetadataFieldEdits(setMeta, unsetMeta)
				if err != nil {
					reportUpdateItemError("metadata edit failed for %s: %v", id, err)
					closeIfUnmutated(result)
					continue
				}
				metaSets, metaUnsets = s, u
			}
			// Handle append_notes: combine existing notes with new content
			if appendNotes, ok := updates["append_notes"].(string); ok {
				combined := issue.Notes
				if combined != "" {
					combined += "\n"
				}
				combined += appendNotes
				regularUpdates["notes"] = combined
			}
			if len(regularUpdates) > 0 {
				if err := issueStore.UpdateIssue(ctx, result.ResolvedID, regularUpdates, actor); err != nil {
					reportUpdateItemError("Error updating %s: %v", id, err)
					closeIfUnmutated(result)
					continue
				}
				trackMutation(result)
				// Audit log key field changes (survives Dolt GC flatten) via the
				// shared cmd-layer chokepoint so every field is captured uniformly
				// (beads-n4sn).
				auditIssueUpdate(result.ResolvedID, issue, regularUpdates, actor, "")
			}

			// Apply per-key metadata edits atomically at the server (beads-fnp6).
			if len(metaSets) > 0 || len(metaUnsets) > 0 {
				if err := issueStore.UpdateMetadataFields(ctx, result.ResolvedID, metaSets, metaUnsets, actor); err != nil {
					reportItemError("metadata edit failed for %s: %v", id, err)
					closeIfUnmutated(result)
					continue
				}
				trackMutation(result)
			}

			// Handle label operations
			var setLabels, addLabels, removeLabels []string
			if v, ok := updates["set_labels"].([]string); ok {
				setLabels = v
			}
			if v, ok := updates["add_labels"].([]string); ok {
				addLabels = v
			}
			if v, ok := updates["remove_labels"].([]string); ok {
				removeLabels = v
			}
			if len(setLabels) > 0 || len(addLabels) > 0 || len(removeLabels) > 0 {
				if err := applyLabelUpdates(ctx, issueStore, result.ResolvedID, actor, setLabels, addLabels, removeLabels); err != nil {
					reportUpdateItemError("Error updating labels for %s: %v", id, err)
					closeIfUnmutated(result)
					continue
				}
				trackMutation(result)
			}

			// Handle parent reparenting
			if newParent, ok := updates["parent"].(string); ok {
				// Validate new parent exists (unless empty string to remove parent)
				if newParent != "" {
					parentIssue, err := issueStore.GetIssue(ctx, newParent)
					if err != nil {
						reportUpdateItemError("Error getting parent %s: %v", newParent, err)
						closeIfUnmutated(result)
						continue
					}
					if parentIssue == nil {
						reportUpdateItemError("parent issue %s not found", newParent)
						closeIfUnmutated(result)
						continue
					}
				}

				// Find and remove ALL existing parent-child dependencies. A
				// child can accumulate multiple parents (e.g. via `bd dep add X
				// Y --type parent-child`, which has no single-parent guard), so
				// removing only the first (the old `break`) left stale parent
				// edges behind and silently corrupted the tree — wrong
				// ready-work/blocked-state/descendant traversal (beads-94ia).
				// --parent reparents to a single parent, so every prior parent
				// edge must go.
				deps, err := issueStore.GetDependencyRecords(ctx, result.ResolvedID)
				if err != nil {
					reportUpdateItemError("Error getting dependencies for %s: %v", id, err)
					closeIfUnmutated(result)
					continue
				}
				for _, dep := range deps {
					if dep.Type != types.DepParentChild {
						continue
					}
					// Skip the edge that already points at the desired parent so
					// we neither drop-and-re-add it nor create a duplicate.
					if dep.DependsOnID == newParent {
						continue
					}
					if err := issueStore.RemoveDependency(ctx, result.ResolvedID, dep.DependsOnID, actor); err != nil {
						reportUpdateItemError("Error removing old parent dependency: %v", err)
					} else {
						trackMutation(result)
					}
				}

				// Add new parent-child dependency (if not removing parent)
				if newParent != "" {
					newDep := &types.Dependency{
						IssueID:     result.ResolvedID,
						DependsOnID: newParent,
						Type:        types.DepParentChild,
					}
					if err := issueStore.AddDependency(ctx, newDep, actor); err != nil {
						reportUpdateItemError("Error adding parent dependency: %v", err)
						closeIfUnmutated(result)
						continue
					}
					trackMutation(result)
				}
			}

			// Re-fetch for display
			updatedIssue, _ := issueStore.GetIssue(ctx, result.ResolvedID)
			updateTitle := ""
			if updatedIssue != nil {
				updateTitle = updatedIssue.Title
			}

			if jsonOutput {
				if updatedIssue != nil {
					updatedIssues = append(updatedIssues, updatedIssue)
				}
			} else if onlyScalarFlags && scalarUpdateIsNoOp(updates, issue) {
				// beads-bdy2: a scalar-only update whose every set field already
				// matches the issue's current value changed nothing — report it
				// honestly instead of a false "✓ Updated". Display-only: the
				// UpdateIssue call above already ran (idempotent, audit intact).
				fmt.Printf("%s %s already matches (no change)\n",
					ui.RenderInfoIcon(), formatFeedbackID(result.ResolvedID, updateTitle))
			} else {
				fmt.Printf("%s Updated issue: %s\n", ui.RenderPass("✓"), formatFeedbackID(result.ResolvedID, updateTitle))
			}

			// Track first successful update for last-touched
			if firstUpdatedID == "" {
				firstUpdatedID = result.ResolvedID
			}
			// This id completed the loop body without a failure `continue`.
			processedCount++
			closeIfUnmutated(result)
		}

		if len(mutatedStores) > 0 {
			for s, ids := range mutatedStores {
				if s == nil {
					continue
				}
				if err := commitPendingIfEmbedded(ctx, s, actor, doltAutoCommitParams{
					Command:  "update",
					IssueIDs: ids,
				}); err != nil {
					closePendingResults()
					return HandleErrorRespectJSON("failed to commit: %v", err)
				}
			}
		}
		closePendingResults()

		// Set last touched after all updates complete
		if firstUpdatedID != "" {
			SetLastTouchedID(firstUpdatedID)
		}

		if jsonOutput && len(updatedIssues) > 0 {
			// Partial success: stdout carries the updated array; flush any
			// deferred per-item failures to stderr as JSON objects (fg6 contract).
			for _, msg := range deferredItemErrors {
				reportItemError("%s", msg)
			}
			if jerr := outputJSON(updatedIssues); jerr != nil {
				return jerr
			}
		}

		if len(args) > 0 && firstUpdatedID == "" {
			// Every requested ID failed. Under --json the deferred per-item
			// errors were intentionally NOT flushed to stderr — the single
			// stdout JSON error object below is the sole error output, so a
			// `2>&1` consumer gets exactly one parseable object (beads-92tz);
			// stdout is never left empty (beads-fg6). Non-JSON exits silently
			// after the per-item stderr lines already printed.
			if jsonOutput {
				return HandleErrorRespectJSON("no issues updated matching the provided IDs")
			}
			return SilentExit()
		}
		if processedCount < len(args) {
			// A partial batch: some ids applied, at least one failed (already
			// reported per-item via reportItemError). Return a non-zero exit so
			// a caller scripting `bd update a b c ...` sees the failure instead
			// of a misleading rc=0, matching `bd close`/`bd delete` which fail
			// on any bad id (beads-4i20). Successes are preserved (their --json
			// output was already emitted above); this only corrects the exit
			// code, and stdout is not touched here so the pure-JSON contract for
			// the emitted successes holds.
			return SilentExit()
		}
		return nil
	},
}

// scalarUpdateIsNoOp reports whether every scalar field set in updates already
// equals the pre-update issue's current value — i.e. the scalar-only update
// changed nothing. Used ONLY for the display feedback line (beads-bdy2), never
// to gate the write. Callers must have already established that updates contains
// ONLY scalar keys (onlyScalarFlags); this compares those keys' values against
// the snapshot the values were computed to be canonical against (status is
// Normalize()d, assignee is normalizeAssignee()d, priority is validated at
// parse time), so a match here means a genuine no-op.
func scalarUpdateIsNoOp(updates map[string]interface{}, issue *types.Issue) bool {
	if issue == nil || len(updates) == 0 {
		return false
	}
	for k, v := range updates {
		switch k {
		case "status":
			s, _ := v.(string)
			if types.Status(s) != issue.Status {
				return false
			}
		case "priority":
			p, ok := v.(int)
			if !ok || p != issue.Priority {
				return false
			}
		case "title":
			t, _ := v.(string)
			if t != issue.Title {
				return false
			}
		case "assignee":
			a, _ := v.(string)
			if a != issue.Assignee {
				return false
			}
		default:
			// Any non-scalar key means this is not a scalar-only no-op; the
			// onlyScalarFlags guard should prevent reaching here, but stay safe.
			return false
		}
	}
	return true
}

// metadataIsJSONObject reports whether raw is a JSON object (or empty/null,
// which the metadata consumers treat as an empty object). Arrays and scalars
// are rejected: they pass json.Valid but every metadata edit path
// (mergeMetadata / applyMetadataEdits / --set-metadata) unmarshals into
// map[string]json.RawMessage and hard-errors on a non-object, permanently
// locking the bead out of all metadata edits (beads-ef2k). Gating at the
// --metadata input sites keeps a non-object from ever being stored.
func metadataIsJSONObject(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return true // empty/null == empty object, tolerated by all consumers
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal([]byte(trimmed), &obj) == nil
}

// mergeMetadata merges new metadata JSON into existing metadata.
// Keys from newMeta overwrite keys in existing; keys only in existing are preserved.
func mergeMetadata(existing, newMeta json.RawMessage) (json.RawMessage, error) {
	base := make(map[string]json.RawMessage)
	if len(existing) > 0 {
		trimmed := strings.TrimSpace(string(existing))
		if trimmed != "" && trimmed != "null" {
			if err := json.Unmarshal(existing, &base); err != nil {
				return nil, fmt.Errorf("existing metadata is not a JSON object: %w", err)
			}
		}
	}

	incoming := make(map[string]json.RawMessage)
	if err := json.Unmarshal(newMeta, &incoming); err != nil {
		return nil, fmt.Errorf("new metadata is not a JSON object: %w", err)
	}

	for k, v := range incoming {
		base[k] = v
	}

	result, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged metadata: %w", err)
	}
	return json.RawMessage(result), nil
}

// parseMetadataFieldEdits validates --set-metadata / --unset-metadata flags and
// returns per-key edits (sets: key → JSON-encoded value; unsets: keys to remove)
// WITHOUT reading or rebuilding the existing metadata blob. The edits are applied
// server-side atomically (issueStore.UpdateMetadataFields → JSON_SET/JSON_REMOVE),
// so concurrent per-key edits to the same issue don't clobber each other via a
// client-side read-modify-write (beads-fnp6). Value typing (number/bool/null)
// matches applyMetadataEdits via toJSONValue.
func parseMetadataFieldEdits(setFlags, unsetFlags []string) (map[string]json.RawMessage, []string, error) {
	var sets map[string]json.RawMessage
	if len(setFlags) > 0 {
		sets = make(map[string]json.RawMessage, len(setFlags))
		for _, kv := range setFlags {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return nil, nil, fmt.Errorf("invalid --set-metadata: expected key=value, got %q", kv)
			}
			if err := storage.ValidateMetadataKey(k); err != nil {
				return nil, nil, err
			}
			sets[k] = toJSONValue(v)
		}
	}
	var unsets []string
	for _, k := range unsetFlags {
		if err := storage.ValidateMetadataKey(k); err != nil {
			return nil, nil, err
		}
		unsets = append(unsets, k)
	}
	return sets, unsets, nil
}

// applyMetadataEdits applies --set-metadata and --unset-metadata edits to existing metadata.
// Returns the merged JSON as json.RawMessage.
// parseMetadataEdits parses --set-metadata (key=value) and --unset-metadata
// (key) flag values into typed per-key edits WITHOUT touching existing
// metadata. It performs the same key validation + value typing (toJSONValue) as
// applyMetadataEdits, but returns the parsed sets/unsets rather than merging
// them into a blob — the merge is done atomically server-side by the proxied
// update path (beads-jibd), avoiding the concurrent-edit clobber that a
// client-side whole-blob read-modify-write causes. A nil map is returned when
// there are no sets (never an empty non-nil map) so downstream len() checks and
// the UpdateSpec no-op path behave.
func parseMetadataEdits(setFlags, unsetFlags []string) (map[string]json.RawMessage, []string, error) {
	var sets map[string]json.RawMessage
	for _, kv := range setFlags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, nil, fmt.Errorf("invalid --set-metadata: expected key=value, got %q", kv)
		}
		if err := storage.ValidateMetadataKey(k); err != nil {
			return nil, nil, err
		}
		if sets == nil {
			sets = make(map[string]json.RawMessage, len(setFlags))
		}
		sets[k] = toJSONValue(v)
	}
	var unsets []string
	for _, k := range unsetFlags {
		if err := storage.ValidateMetadataKey(k); err != nil {
			return nil, nil, err
		}
		unsets = append(unsets, k)
	}
	return sets, unsets, nil
}

func applyMetadataEdits(existing json.RawMessage, setFlags, unsetFlags []string) (json.RawMessage, error) {
	// Parse existing metadata (or start with empty object)
	data := make(map[string]json.RawMessage)
	if len(existing) > 0 {
		trimmed := strings.TrimSpace(string(existing))
		if trimmed != "" && trimmed != "null" {
			if err := json.Unmarshal(existing, &data); err != nil {
				return nil, fmt.Errorf("existing metadata is not a JSON object: %w", err)
			}
		}
	}

	// Apply --set-metadata key=value pairs
	for _, kv := range setFlags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --set-metadata: expected key=value, got %q", kv)
		}
		if err := storage.ValidateMetadataKey(k); err != nil {
			return nil, err
		}
		// Store as JSON value: try to preserve type (number, bool, null)
		data[k] = toJSONValue(v)
	}

	// Apply --unset-metadata keys
	for _, k := range unsetFlags {
		if err := storage.ValidateMetadataKey(k); err != nil {
			return nil, err
		}
		delete(data, k)
	}

	result, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	return json.RawMessage(result), nil
}

// toJSONValue converts a string value to its most appropriate JSON representation.
// Recognizes numbers, booleans, and null; everything else becomes a JSON string.
func toJSONValue(s string) json.RawMessage {
	// Check for null
	if s == "null" {
		return json.RawMessage("null")
	}
	// Check for booleans
	if s == "true" || s == "false" {
		return json.RawMessage(s)
	}
	// Check for numbers (integer or float). Only coerce when the value is a
	// canonical JSON number that round-trips LOSSLESSLY — otherwise a big
	// integer (snowflake/gh:run ID, >15-16 significant digits) would be stored
	// as a lossy float, and a whitespace-padded or non-canonical form ("  3  ",
	// "5.0") would silently change the user's value (beads-nj8y). Auto-typing of
	// clean numbers (e.g. story_points=5) stays a blessed feature.
	if isLosslessJSONNumber(s) {
		return json.RawMessage(s)
	}
	// Default to JSON string
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}

// isLosslessJSONNumber reports whether s should be stored as a JSON number.
// It requires that s is a canonical JSON number literal AND that it survives a
// full round-trip through the float64 that a standard JSON consumer
// (json.Unmarshal into interface{}) will read back — because that is how the
// stored metadata is later decoded. This rejects:
//   - non-canonical forms ("  3  ", "3 ", "5.0") — literal != re-encoding
//   - integers beyond float64's exact range (>2^53, e.g. 18-30 digit IDs) —
//     they'd read back as a lossy float, corrupting the value (beads-nj8y)
//
// while accepting clean small numbers (5, -3, 1.5) so the story_points=5 style
// auto-typing stays intact.
func isLosslessJSONNumber(s string) bool {
	// Must be a canonical number literal: the json.Number token must equal the
	// entire input (rejects surrounding whitespace and trailing garbage).
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var num json.Number
	if err := dec.Decode(&num); err != nil || num.String() != s || dec.More() {
		return false
	}
	// The value must round-trip through float64 (how it is later read back) with
	// no loss: re-encoding the parsed float must reproduce the original literal.
	f, err := num.Float64()
	if err != nil {
		return false
	}
	b, err := json.Marshal(f)
	if err != nil || string(b) != s {
		return false
	}
	return true
}

func init() {
	updateCmd.Flags().StringP("status", "s", "", "New status")
	registerPriorityFlag(updateCmd, "")
	updateCmd.Flags().String("title", "", "New title")
	updateCmd.Flags().StringP("type", "t", "", "New type (bug|feature|task|epic|chore|decision); custom types require types.custom config")
	registerCommonIssueFlags(updateCmd)
	updateCmd.Flags().Bool("allow-empty-description", false, "Allow empty description replacement when reading from stdin or file")
	updateCmd.Flags().String("spec-id", "", "Link to specification document")
	updateCmd.Flags().String("acceptance-criteria", "", "DEPRECATED: use --acceptance")
	_ = updateCmd.Flags().MarkHidden("acceptance-criteria") // Only fails if flag missing (caught in tests)
	updateCmd.Flags().IntP("estimate", "e", 0, "Time estimate in minutes (e.g., 60 for 1 hour)")
	updateCmd.Flags().StringSlice("add-label", nil, "Add labels (repeatable)")
	updateCmd.Flags().StringSlice("remove-label", nil, "Remove labels (repeatable)")
	updateCmd.Flags().StringSlice("set-labels", nil, "Set labels, replacing all existing (repeatable)")
	updateCmd.Flags().String("parent", "", "New parent issue ID (reparents the issue, use empty string to remove parent)")
	updateCmd.Flags().Bool("claim", false, "Atomically claim the issue (sets assignee to you, status to in_progress; idempotent if already claimed by you)")
	updateCmd.Flags().Bool("force", false, "Override close-time integrity guards when setting --status closed (blocked-by-open, epic-with-open-children); mirrors 'bd close --force'")
	updateCmd.Flags().String("session", "", "Claude Code session ID for status=closed (or set CLAUDE_SESSION_ID env var)")
	// Time-based scheduling flags (GH#820)
	// Examples:
	//   --due=+6h           Due in 6 hours
	//   --due=tomorrow      Due tomorrow
	//   --due="next monday" Due next Monday
	//   --due=2025-01-15    Due on specific date
	//   --due=""            Clear due date
	//   --defer=+1h         Hidden from bd ready for 1 hour
	//   --defer=""          Clear defer (show in bd ready immediately)
	updateCmd.Flags().String("due", "", "Due date/time (empty to clear). Formats: +6h, +1d, +2w, tomorrow, next monday, 2025-01-15")
	updateCmd.Flags().String("defer", "", "Defer until date (empty to clear). Issue hidden from bd ready until then")
	// Gate fields (bd-z6kw)
	updateCmd.Flags().String("await-id", "", "Set gate await_id (e.g., GitHub run ID for gh:run gates)")
	// Ephemeral/persistent flags
	updateCmd.Flags().Bool("ephemeral", false, "Mark issue as ephemeral (wisp) - not exported to JSONL")
	updateCmd.Flags().Bool("persistent", false, "Mark issue as persistent (promote wisp to regular issue)")
	updateCmd.Flags().Bool("no-history", false, "Mark issue as no-history (skip Dolt commits, not GC-eligible)")
	updateCmd.Flags().Bool("history", false, "Clear no-history flag (re-enable Dolt commit history)")
	updateCmd.Flags().Bool("pinned", false, "Pin issue as a persistent context marker (protected from prune/purge)")
	updateCmd.Flags().Bool("no-pinned", false, "Clear the pinned context marker")
	// Metadata flag (GH#1413)
	updateCmd.Flags().String("metadata", "", "Set custom metadata (JSON string or @file.json to read from file)")
	// Incremental metadata edits (GH#1406)
	updateCmd.Flags().StringArray("set-metadata", nil, "Set metadata key=value (repeatable, e.g., --set-metadata team=platform)")
	updateCmd.Flags().StringArray("unset-metadata", nil, "Remove metadata key (repeatable, e.g., --unset-metadata team)")
	updateCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(updateCmd)
}
