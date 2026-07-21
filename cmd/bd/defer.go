package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/timeparsing"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var deferCmd = &cobra.Command{
	Use:   "defer [id...]",
	Short: "Defer one or more issues for later",
	Long: `Defer issues to put them on ice for later.

Deferred issues are deliberately set aside - not blocked by anything specific,
just postponed for future consideration. Unlike blocked issues, there's no
dependency keeping them from being worked. Unlike closed issues, they will
be revisited.

Deferred issues don't show in 'bd ready' but remain visible in 'bd list'.

Examples:
  bd defer bd-abc                  # Defer a single issue (status-based)
  bd defer bd-abc --until=tomorrow # Defer until specific time
  bd defer bd-abc --reason="waiting on API access"
  bd defer bd-abc bd-def           # Defer multiple issues`,
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("defer")

		evt := metrics.NewCommandEvent("defer")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		var deferUntil *time.Time
		// beads-jy4r9 leg A: a PAST --until date must NOT flip status to deferred
		// (the issue re-appears in bd ready immediately because the ready predicate
		// ignores a past defer_until). inPast is true only when a --until date was
		// given AND it is in the past; a dateless `bd defer` (deferUntil==nil) stays
		// unconditionally deferred. Mirrors update.go's !inPast guard so the two
		// defer entry points agree (update --defer <past> already keeps status=open).
		inPast := false
		untilStr, _ := cmd.Flags().GetString("until")
		if untilStr != "" {
			t, err := timeparsing.ParseRelativeTime(untilStr, time.Now())
			if err != nil {
				// beads-v02z: defer honors --json on success (outputJSON below) and
				// already routes its ID-resolution error through HandleErrorRespectJSON
				// (beads-0l4c) — honor the same --json error contract on this shared
				// validation path (runs before the direct/proxied split below) so
				// `bd defer --until=garbage --json` emits a stdout JSON error object,
				// not empty stdout + stderr text (0wp9/21xi class).
				return HandleErrorRespectJSON("invalid --until format %q. Examples: +1h, tomorrow, next monday, 2025-01-15", untilStr)
			}
			inPast = t.Before(time.Now())
			if inPast {
				// beads-jy4r9 leg B: the "appears in bd ready immediately" warning was
				// suppressed under --json and surfaced NOWHERE, so a --json consumer got
				// zero signal the defer is an immediate no-op. Emit it as a JSON object
				// on STDERR under --json (stdout stays the pure issue-array success
				// payload, matching reportItemError/jsonStderrError convention); plain
				// text on stderr otherwise (unchanged human behavior).
				if jsonOutput {
					jsonStderrError(
						fmt.Sprintf("Defer date %q is in the past; issue stays status=open and appears in bd ready immediately. Did you mean a future date?", t.Format("2006-01-02 15:04")),
						"Use --until=+1h or --until=tomorrow for a future defer date")
				} else {
					fmt.Fprintf(os.Stderr, "%s Defer date %q is in the past. Issue will appear in bd ready immediately.\n",
						ui.RenderWarn("!"), t.Format("2006-01-02 15:04"))
					fmt.Fprintf(os.Stderr, "  Did you mean a future date? Use --until=+1h or --until=tomorrow\n")
				}
			}
			deferUntil = &t
		}
		reason, _ := cmd.Flags().GetString("reason")
		reason = strings.TrimSpace(reason)
		if cmd.Flags().Changed("reason") && reason == "" {
			// beads-v02z: same --json error contract as the --until path above.
			return HandleErrorRespectJSON("reason cannot be empty")
		}

		// beads-aocj: route to the proxied handler in proxied-server mode.
		// Without this, defer uses the direct global `store` — nil under
		// proxiedServerMode — so `bd defer` failed "storage is nil" (or "database
		// not initialized" at the nil-guard below), unlike `bd update --defer`
		// which routes via usesProxiedServer(). Parse --until/--reason first so
		// the past-date warning + reason validation still fire identically.
		if usesProxiedServer() {
			return runDeferProxiedServer(rootCtx, args, deferUntil, inPast, reason)
		}

		ctx := rootCtx

		_, err := utils.ResolvePartialIDs(ctx, store, args)
		if err != nil {
			// Respect --json: an unresolvable ID must emit a stdout JSON error
			// object (beads-0l4c), not plain text to stderr, so --json consumers
			// can parse the failure instead of seeing empty stdout + exit 1.
			return HandleErrorRespectJSON("%v", err)
		}

		deferredIssues := []*types.Issue{}
		deferredCount := 0
		alreadyDeferredCount := 0

		if store == nil {
			return HandleErrorWithHint("database not initialized", diagHint())
		}

		for _, id := range args {
			fullID, err := utils.ResolvePartialID(ctx, store, id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving %s: %v\n", id, err)
				continue
			}

			// beads-jy4r9 leg A: set status=deferred UNLESS a past --until date was
			// given (then keep the issue ready-visible with status=open, matching
			// update --defer <past>). A dateless defer or a future --until still
			// defers. deferredStatus is the status this command actually transitions
			// to — used for both the update and the audit-trail entry so they agree.
			deferredStatus := string(types.StatusDeferred)
			if deferUntil != nil && inPast {
				deferredStatus = string(types.StatusOpen)
			}
			updates := map[string]interface{}{
				"status": deferredStatus,
			}
			if deferUntil != nil {
				updates["defer_until"] = *deferUntil
			}
			// Load the issue up front for the pre-change status (audit trail) and
			// for appending the reason to notes. Fall back to "open" if the load
			// fails but the update later succeeds, matching close.go's default.
			oldStatus := "open"
			if issue, gerr := store.GetIssue(ctx, fullID); gerr == nil && issue != nil {
				oldStatus = string(issue.Status)
				// Already-deferred guard (beads-fs01): `bd defer` on an issue that
				// is already status=deferred, with NO new --until and NO --reason,
				// changes nothing — yet the command printed "* Deferred" (rc=0), a
				// false success a CI/agent gate re-running defer reads as proof of a
				// state change. Mirror the landed close (beads-dr3) / reopen
				// (beads-b0tw) already-in-state no-op guards: report an honest "already
				// deferred, no change" and skip the write (no spurious audit event /
				// commit). A re-defer WITH a new --until or --reason is a genuine
				// change (defer_until / notes) and still falls through to succeed.
				if issue.Status == types.StatusDeferred && deferUntil == nil && reason == "" {
					alreadyDeferredCount++
					if jsonOutput {
						// JSON reflects state, not a claimed transition: the issue
						// is (still) deferred, so keep it in the payload.
						deferredIssues = append(deferredIssues, issue)
					} else {
						fmt.Printf("%s %s was already deferred (no change)\n",
							ui.RenderInfoIcon(), formatFeedbackID(fullID, issue.Title))
					}
					continue
				}
				// NOTE: the --reason append is NOT folded into `updates["notes"]`
				// here. A client-side read (issue.Notes) → concat → whole-blob write
				// is a read-modify-write that loses a concurrent notes append
				// (another defer --reason, an `update --append-notes`, or set-state)
				// landing between the GetIssue above and the UpdateIssue below —
				// last-writer-wins on the whole notes column, silent. Instead the
				// reason is appended ATOMICALLY at the DB via store.AppendNotes (a
				// single server-side CONCAT_WS) after the scalar update, mirroring
				// `bd update --append-notes` (beads-jscve). The defer sink twin of
				// that fix (beads-j8yhg).
			} else if reason != "" {
				// Reason requested but the issue couldn't be loaded: fail rather
				// than silently drop the reason (prior behavior).
				if gerr != nil {
					fmt.Fprintf(os.Stderr, "Error loading %s: %v\n", fullID, gerr)
				} else {
					fmt.Fprintf(os.Stderr, "Issue %s not found\n", fullID)
				}
				continue
			}

			if err := store.UpdateIssue(ctx, fullID, updates, actor); err != nil {
				fmt.Fprintf(os.Stderr, "Error deferring %s: %v\n", fullID, err)
				continue
			}
			// Append the --reason ATOMICALLY at the DB (beads-j8yhg) — a single
			// server-side CONCAT_WS in its own transaction — instead of the old
			// client-side read/concat/whole-blob write, which lost a concurrent
			// notes append (last-writer-wins, silent). Applied as its own write
			// after the scalar UpdateIssue so it can't clobber a concurrent append
			// via UpdateIssue's full-row write. Mirrors `bd update --append-notes`.
			if reason != "" {
				if err := store.AppendNotes(ctx, fullID, reason, actor); err != nil {
					fmt.Fprintf(os.Stderr, "Error appending reason for %s: %v\n", fullID, err)
					continue
				}
			}
			// Audit log the defer status change (survives Dolt GC flatten) via
			// the shared cmd-layer chokepoint (beads-n4sn). Uses the ACTUAL target
			// status (deferredStatus) so a past-date defer records the truthful
			// open->open (or prior->open) transition, not a phantom ->deferred
			// (beads-jy4r9 leg A).
			auditStatusChange(fullID, oldStatus, deferredStatus, actor, reason)
			deferredCount++

			if jsonOutput {
				issue, _ := store.GetIssue(ctx, fullID)
				if issue != nil {
					deferredIssues = append(deferredIssues, issue)
				}
			} else if deferUntil != nil && inPast {
				// Truthful feedback: the issue is scheduled but stays ready now.
				fmt.Printf("%s Scheduled %s for %s (past date — stays in bd ready now)\n",
					ui.RenderAccent("*"), fullID, deferUntil.Format("2006-01-02 15:04"))
			} else {
				fmt.Printf("%s Deferred %s\n", ui.RenderAccent("*"), fullID)
			}
		}

		if jsonOutput && len(deferredIssues) > 0 {
			if err := outputJSON(deferredIssues); err != nil {
				return err
			}
		}

		if deferredCount > 0 {
			commandDidWrite.Store(true)
		}

		// Every requested ID failed (per-item errors already printed to
		// stderr): exit non-zero so callers/scripts don't read false success.
		// Under --json, stdout is still empty here, so emit a stdout JSON error
		// object to keep the failure parseable (beads-fg6, matching the update
		// and close batch paths) instead of a bare silent exit. Partial success
		// (deferredCount>0) keeps rc=0 and its JSON array above, mirroring
		// update/close batch semantics. An all-idempotent-no-op batch
		// (alreadyDeferredCount>0, beads-fs01) also keeps rc=0 — nothing failed,
		// matching close.go's already-closed exclusion (beads-dr3).
		if len(args) > 0 && deferredCount == 0 && alreadyDeferredCount == 0 {
			if jsonOutput {
				return HandleErrorRespectJSON("no issues deferred matching the provided IDs")
			}
			return SilentExit()
		}
		return nil
	},
}

func init() {
	// Time-based scheduling flag (GH#820)
	deferCmd.Flags().String("until", "", "Defer until specific time (e.g., +1h, tomorrow, next monday)")
	deferCmd.Flags().String("reason", "", "Record why this issue is being deferred (appended to notes)")
	deferCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(deferCmd)
}
