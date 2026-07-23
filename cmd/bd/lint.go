package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/validation"
)

// LintResult holds the validation result for a single issue.
type LintResult struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Missing  []string `json:"missing,omitempty"`
	Warnings int      `json:"warnings"`
}

// InconsistencyResult flags a structural invariant violation on a single issue,
// separate from the template-section warnings above. beads-4u7d: a closed epic
// that still has one or more open (non-closed) parent-child children violates the
// "a closed epic has no open children" invariant. The guard family (beads-2hkd
// demote, beads-b0tw child-reopen, beads-eth8 dep-add, epic-close in close.go)
// prevents that state being REACHED going forward, but this lint FLAGS any
// existing instance regardless of how it arose — reached via --force operator
// override, a not-yet-guarded mutation path, or a pre-existing row from before the
// guards landed.
type InconsistencyResult struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Kind         string   `json:"kind"` // e.g. "closed_epic_with_open_children"
	OpenChildren []string `json:"open_children,omitempty"`
}

var lintCmd = &cobra.Command{
	Use:     "lint [issue-id...]",
	GroupID: "views",
	Short:   "Check issues for missing template sections",
	Long: `Check issues for missing recommended sections based on issue type.

By default, lints all open issues. Specify issue IDs to lint specific issues.

Section requirements by type:
  bug:      Steps to Reproduce, Acceptance Criteria
  task:     Acceptance Criteria
  feature:  Acceptance Criteria
  epic:     Success Criteria
  chore:    (none)

Examples:
  bd lint                    # Lint all open issues
  bd lint bd-abc             # Lint specific issue
  bd lint bd-abc bd-def      # Lint multiple issues
  bd lint --type bug         # Lint only bugs
  bd lint --status all       # Lint all issues (including closed)
`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("lint")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		ctx := rootCtx

		typeFilter, _ := cmd.Flags().GetString("type")
		statusFilter, _ := cmd.Flags().GetString("status")

		var issues []*types.Issue

		// beads-hquo8: route all reads through a backend that uses the proxied UOW
		// for hub (proxiedServerMode) crew — where the global `store` is nil — and
		// the direct store otherwise. Before this, lint hard-failed "database not
		// initialized" for hub crew, breaking `bd lint $IDS || fail` as a CI gate
		// (read-divergence sibling of info/6pjl6, orphans/ktlo, count, show).
		backend := newLintBackend(ctx)
		defer backend.close()
		if backend.uw == nil && store == nil {
			return HandleErrorWithHint("database not initialized", diagHint())
		}

		// failedCount tracks ids that could not be resolved/loaded in the
		// explicit-args path. lint is used as a gate (`bd lint $IDS || fail`),
		// so a typo'd or deleted id must make the whole command exit non-zero
		// rather than silently linting the ids that happened to resolve — the
		// partial-failure exit-code class the rest of the multi-id commands
		// already honor (beads-p3y5, matching label/show/dep list/mol burn).
		failedCount := 0
		if len(args) > 0 {
			for _, id := range args {
				issue, err := backend.getIssue(ctx, id)
				if err != nil {
					// beads-iwy1k: lint ALWAYS emits its results
					// envelope on stdout under --json (below), so a per-id
					// resolve failure must NOT leak plaintext to stderr — that
					// pollutes `bd lint $IDS --json 2>&1 | jq`. Route through the
					// json-aware reportItemError (errors.go:250): a JSON error
					// object to stderr under --json, the plain line otherwise.
					// This is the clean-stderr per-item contract the fg6/92tz/
					// en28/n96g family established for show/update/label/reopen/
					// undefer/close — lint's args loop (beads-p3y5) was the
					// odd-one-out still writing raw stderr.
					reportItemError("Error getting %s: %v", id, err)
					failedCount++
					continue
				}
				if issue == nil {
					reportItemError("Issue not found: %s", id)
					failedCount++
					continue
				}
				issues = append(issues, issue)
			}
		} else {
			filter := types.IssueFilter{}

			// beads-8cg2: validate --status/--type against their documented
			// enums, mirroring bd list, so an invalid value is a hard error
			// (rc!=0) instead of silently linting 0 issues (a false-clean pass
			// a typo'd CI/agent lint gate would read as success). Custom sets
			// come from store config so custom statuses + infra types pass.
			// (backend is guaranteed initialized by the guard above.)
			lintFilterCfg, lintCfgErr := backend.loadFilterConfig(ctx)
			if lintCfgErr != nil {
				// beads-21xi: runs before the `if jsonOutput` block below, so under
				// `bd lint --json` a plain HandleError left stdout empty + stderr text
				// — honor the --json error contract, matching the sibling status/type
				// validation returns just below (0wp9/yw6g class).
				return HandleErrorRespectJSON("%v", lintCfgErr)
			}

			if statusFilter == "" || statusFilter == "open" {
				s := types.StatusOpen
				filter.Status = &s
			} else if statusFilter != "all" {
				s := types.Status(statusFilter).Normalize()
				if !s.IsValidWithCustom(lintFilterCfg.customStatusNames()) {
					return HandleErrorRespectJSON("invalid status %q (valid: %s)", statusFilter, validStatusList(lintFilterCfg.customStatusNames()))
				}
				if s == types.StatusBlocked {
					// beads-pbelp: "blocked" is a derived pseudo-status (the
					// is_blocked column), not a stored status value, so setting
					// filter.Status = "blocked" builds `status = 'blocked'` which
					// matches nothing — lint would silently check 0 issues under
					// `--status blocked` (a false "clean"). Route to filter.Blocked
					// so it reaches the is_blocked=1 predicate (beads-7f3g), exactly
					// as bd count (count.go) and bd list do. Sibling of the stale
					// path (beads-h40fl). --status blocked stays a VALID value
					// (de4r/8cg2); only its translation to the query is fixed.
					b := true
					filter.Blocked = &b
				} else {
					filter.Status = &s
				}
			}

			if typeFilter != "" {
				t := issueTypeFilterValue(typeFilter)
				if !t.IsValidWithCustom(lintFilterCfg.customTypes) {
					validTypes := types.ValidWorkTypesString() // beads-71j1: full 9-type list, not a stale hardcoded 6
					if len(lintFilterCfg.customTypes) > 0 {
						validTypes += ", " + strings.Join(lintFilterCfg.customTypes, ", ")
					}
					return HandleErrorRespectJSON("invalid issue type %q (valid: %s)", typeFilter, validTypes)
				}
				filter.IssueType = &t
			}

			var err error
			issues, err = backend.searchIssues(ctx, filter)
			if err != nil {
				// beads-21xi: honor the --json error contract on this store-error
				// path too (empty stdout + stderr text under `bd lint --json`).
				return HandleErrorRespectJSON("%v", err)
			}
		}

		// Structural inconsistency scan (beads-4u7d), orthogonal to the template
		// warnings below. When explicit IDs were given, only those are checked;
		// otherwise scan every closed epic (not just the default open template set).
		inconsistencies := scanClosedEpicsWithOpenChildren(ctx,
			backend, closedEpicsToScan(ctx, backend, issues, len(args) > 0))

		var results []LintResult
		totalWarnings := 0

		for _, issue := range issues {
			err := validation.LintIssue(issue)
			if err == nil {
				continue
			}

			templateErr, ok := err.(*validation.TemplateError)
			if !ok {
				continue
			}

			missing := make([]string, len(templateErr.Missing))
			for i, m := range templateErr.Missing {
				missing[i] = m.Heading
			}

			result := LintResult{
				ID:       issue.ID,
				Title:    issue.Title,
				Type:     string(issue.IssueType),
				Missing:  missing,
				Warnings: len(missing),
			}
			results = append(results, result)
			totalWarnings += len(missing)
		}

		if jsonOutput {
			// beads-tamf: normalize nil→[] so the nested "results" field marshals
			// to a JSON [] (not null) on a clean db, matching the array contract.
			if results == nil {
				results = []LintResult{}
			}
			output := struct {
				Total           int                   `json:"total"`
				Issues          int                   `json:"issues"`
				Results         []LintResult          `json:"results"`
				Inconsistencies []InconsistencyResult `json:"inconsistencies,omitempty"`
			}{
				Total:           totalWarnings,
				Issues:          len(results),
				Results:         results,
				Inconsistencies: inconsistencies,
			}
			// beads-s2oy: outputJSON for schema_version + BD_JSON_ENVELOPE.
			if err := outputJSON(output); err != nil {
				return err
			}
			// Partial/total id-resolution failure: results (above) are still
			// emitted for the ids that resolved, then signal non-zero so a
			// scripted `bd lint $IDS --json || fail` fires on a missing id
			// (beads-p3y5).
			if failedCount > 0 {
				return &exitError{Code: 1}
			}
			// beads-x3jo: mirror the text-mode exit contract. Text mode returns
			// SilentExit() (rc=1) when there are template warnings or structural
			// inconsistencies, so a `bd lint $IDS || fail` gate trips. The --json
			// branch previously returned nil (rc=0) on the SAME warning state, so
			// `bd lint $IDS --json || fail` read FALSE-CLEAN — a scripted gate
			// silently weakened by adding --json. Signal non-zero here too so both
			// modes agree; the JSON object is already emitted above, so consumers
			// still parse the findings.
			if len(results) > 0 || len(inconsistencies) > 0 {
				return &exitError{Code: 1}
			}
			return nil
		}

		if len(results) == 0 && len(inconsistencies) == 0 {
			fmt.Printf("✓ No template warnings found (%d issues checked)\n", len(issues))
			if failedCount > 0 {
				return &exitError{Code: 1}
			}
			return nil
		}

		if len(results) > 0 {
			renderLintTemplateWarnings(results, totalWarnings)
		}

		if len(inconsistencies) > 0 {
			renderLintInconsistencies(inconsistencies)
		}

		// Warnings/inconsistencies (above) are still printed for the ids that
		// resolved; signal non-zero if any requested id failed to resolve so a
		// lint gate fires on a missing id (beads-p3y5).
		if failedCount > 0 {
			return &exitError{Code: 1}
		}
		return SilentExit()
	},
}

// renderLintTemplateWarnings prints the text-mode template-warning block.
// beads-v2npj: the issue Title is store-read and can carry OSC/CSI escapes from
// an untrusted import (JSONL/markdown/SCM), so it is routed through displayTitle
// (ui.SanitizeForTerminal) before terminal display — the same 7n9y sink-class
// fix as show/create/epic. Display-only: the --json path (LintResult.Title)
// stays raw for round-trip fidelity.
func renderLintTemplateWarnings(results []LintResult, totalWarnings int) {
	fmt.Printf("Template warnings (%d issues, %d warnings):\n\n", len(results), totalWarnings)
	for _, r := range results {
		fmt.Printf("%s [%s]: %s\n", r.ID, r.Type, displayTitle(r.Title))
		for _, m := range r.Missing {
			fmt.Printf("  ⚠ Missing: %s\n", m)
		}
		fmt.Println()
	}
}

// renderLintInconsistencies prints the text-mode structural-inconsistency block.
// beads-v2npj: sanitize the store-read Title for terminal display (see
// renderLintTemplateWarnings); the --json path (InconsistencyResult.Title) stays
// raw.
func renderLintInconsistencies(inconsistencies []InconsistencyResult) {
	fmt.Printf("Structural inconsistencies (%d):\n\n", len(inconsistencies))
	for _, inc := range inconsistencies {
		fmt.Printf("%s [epic]: %s\n", inc.ID, displayTitle(inc.Title))
		fmt.Printf("  ✗ closed epic with %d open child issue(s): %v\n", len(inc.OpenChildren), inc.OpenChildren)
		fmt.Println()
	}
}

// openChildIDsOfEpic returns the IDs of an epic's open (non-closed) parent-child
// children. It is the ID-returning sibling of countEpicOpenChildren (close.go),
// which returns only a count; the lint surfaces the offending child IDs so the
// operator can act. Best-effort: a lookup error yields no children (fail-open),
// matching countEpicOpenChildren's posture.
func openChildIDsOfEpic(ctx context.Context, s storage.DoltStorage, epicID string) []string {
	dependents, err := s.GetDependentsWithMetadata(ctx, epicID)
	if err != nil {
		return nil
	}
	// beads-97gmg: keep the lint's "closed epic with open children" scan in
	// lockstep with the close guard (countEpicOpenChildren) — a done-category
	// child counts as complete, so it is NOT an offending open child. Otherwise
	// closing an epic whose remaining children are all done-category (now
	// permitted) would immediately trip this lint as a false inconsistency.
	done := doneCategoryStatusNames(ctx, s)
	var open []string
	for _, dep := range dependents {
		if dep.DependencyType == types.DepParentChild && childCountsAsOpen(dep.Issue.Status, done) {
			open = append(open, dep.Issue.ID)
		}
	}
	return open
}

// scanClosedEpicsWithOpenChildren finds every closed epic in the given issue set
// that still has at least one open parent-child child (beads-4u7d). The caller
// passes the issue set to scan: when the user lints specific IDs, only those are
// checked; otherwise the lint command scans ALL closed epics (independent of the
// template --status/--type filter, since a closed epic is invisible under the
// default --status=open scan). Returns one InconsistencyResult per offending epic.
func scanClosedEpicsWithOpenChildren(ctx context.Context, b *lintBackend, issues []*types.Issue) []InconsistencyResult {
	// beads-ulsg4: a parent in a custom done-category status is terminal too, so
	// lint must flag a done-category epic that still has open children — the
	// close/reopen/reparent guards now treat it as closed, so lint (the post-hoc
	// detector) MUST match or the guard-bypass becomes undetectable. Degraded-safe:
	// an empty done-set reduces to the prior literal-'closed' scan.
	done := b.doneCategoryStatusSet(ctx)
	var results []InconsistencyResult
	for _, issue := range issues {
		if issue.IssueType != types.TypeEpic || !parentStatusIsTerminal(issue.Status, done) {
			continue
		}
		openChildren := b.openChildIDsOfEpic(ctx, issue.ID)
		if len(openChildren) == 0 {
			continue
		}
		results = append(results, InconsistencyResult{
			ID:           issue.ID,
			Title:        issue.Title,
			Kind:         "closed_epic_with_open_children",
			OpenChildren: openChildren,
		})
	}
	return results
}

// closedEpicsToScan returns the set of closed epics the inconsistency scan should
// check. When explicit args were given, the scan is limited to those (already in
// `issues`); otherwise it queries every closed epic, independent of the template
// lint's --status/--type filter — a closed epic never appears under the default
// --status=open scan, so relying on the template issue set would make beads-4u7d
// silently no-op in the common case.
func closedEpicsToScan(ctx context.Context, b *lintBackend, explicitIssues []*types.Issue, hadArgs bool) []*types.Issue {
	if hadArgs {
		return explicitIssues
	}
	epic := types.TypeEpic
	// beads-ulsg4: also scan epics in a custom done-category status — they are
	// terminal (the close-guard family now treats them so), so a done-category
	// epic with open children is a real inconsistency lint must surface. Query
	// via the Statuses OR-filter (closed + every configured done-category name);
	// an empty done-set falls back to a single-status closed query, byte-identical
	// to before.
	done := b.doneCategoryStatusSet(ctx)
	if len(done) == 0 {
		closed := types.StatusClosed
		closedEpics, err := b.searchIssues(ctx, types.IssueFilter{Status: &closed, IssueType: &epic})
		if err != nil {
			return nil
		}
		return closedEpics
	}
	statuses := []types.Status{types.StatusClosed}
	for name := range done {
		statuses = append(statuses, types.Status(name))
	}
	closedEpics, err := b.searchIssues(ctx, types.IssueFilter{Statuses: statuses, IssueType: &epic})
	if err != nil {
		return nil
	}
	return closedEpics
}

func init() {
	lintCmd.Flags().StringP("type", "t", "", "Filter by issue type (bug, task, feature, epic)")
	lintCmd.Flags().StringP("status", "s", "", "Filter by status (default: open, use 'all' for all)")

	rootCmd.AddCommand(lintCmd)
}
