package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

		if store == nil {
			return HandleErrorWithHint("database not initialized", diagHint())
		}

		if len(args) > 0 {
			for _, id := range args {
				issue, err := store.GetIssue(ctx, id)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error getting %s: %v\n", id, err)
					continue
				}
				if issue == nil {
					fmt.Fprintf(os.Stderr, "Issue not found: %s\n", id)
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
			// (store is guaranteed non-nil by the guard above.)
			lintFilterCfg, lintCfgErr := loadDirectListFilterConfig(ctx, store)
			if lintCfgErr != nil {
				return HandleError("%v", lintCfgErr)
			}

			if statusFilter == "" || statusFilter == "open" {
				s := types.StatusOpen
				filter.Status = &s
			} else if statusFilter != "all" {
				s := types.Status(statusFilter).Normalize()
				if !s.IsValidWithCustom(lintFilterCfg.customStatusNames()) {
					return HandleErrorRespectJSON("invalid status %q (valid: %s)", statusFilter, validStatusList(lintFilterCfg.customStatusNames()))
				}
				filter.Status = &s
			}

			if typeFilter != "" {
				t := issueTypeFilterValue(typeFilter)
				if !t.IsValidWithCustom(lintFilterCfg.customTypes) {
					validTypes := "bug, feature, task, epic, chore, decision"
					if len(lintFilterCfg.customTypes) > 0 {
						validTypes += ", " + strings.Join(lintFilterCfg.customTypes, ", ")
					}
					return HandleErrorRespectJSON("invalid issue type %q (valid: %s)", typeFilter, validTypes)
				}
				filter.IssueType = &t
			}

			var err error
			issues, err = store.SearchIssues(ctx, "", filter)
			if err != nil {
				return HandleError("%v", err)
			}
		}

		// Structural inconsistency scan (beads-4u7d), orthogonal to the template
		// warnings below. When explicit IDs were given, only those are checked;
		// otherwise scan every closed epic (not just the default open template set).
		inconsistencies := scanClosedEpicsWithOpenChildren(ctx,
			store, closedEpicsToScan(ctx, store, issues, len(args) > 0))

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
			data, _ := json.MarshalIndent(output, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(results) == 0 && len(inconsistencies) == 0 {
			fmt.Printf("✓ No template warnings found (%d issues checked)\n", len(issues))
			return nil
		}

		if len(results) > 0 {
			fmt.Printf("Template warnings (%d issues, %d warnings):\n\n", len(results), totalWarnings)
			for _, r := range results {
				fmt.Printf("%s [%s]: %s\n", r.ID, r.Type, r.Title)
				for _, m := range r.Missing {
					fmt.Printf("  ⚠ Missing: %s\n", m)
				}
				fmt.Println()
			}
		}

		if len(inconsistencies) > 0 {
			fmt.Printf("Structural inconsistencies (%d):\n\n", len(inconsistencies))
			for _, inc := range inconsistencies {
				fmt.Printf("%s [epic]: %s\n", inc.ID, inc.Title)
				fmt.Printf("  ✗ closed epic with %d open child issue(s): %v\n", len(inc.OpenChildren), inc.OpenChildren)
				fmt.Println()
			}
		}

		return SilentExit()
	},
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
	var open []string
	for _, dep := range dependents {
		if dep.DependencyType == types.DepParentChild && dep.Issue.Status != types.StatusClosed {
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
func scanClosedEpicsWithOpenChildren(ctx context.Context, s storage.DoltStorage, issues []*types.Issue) []InconsistencyResult {
	var results []InconsistencyResult
	for _, issue := range issues {
		if issue.IssueType != types.TypeEpic || issue.Status != types.StatusClosed {
			continue
		}
		openChildren := openChildIDsOfEpic(ctx, s, issue.ID)
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
func closedEpicsToScan(ctx context.Context, s storage.DoltStorage, explicitIssues []*types.Issue, hadArgs bool) []*types.Issue {
	if hadArgs {
		return explicitIssues
	}
	closed := types.StatusClosed
	epic := types.TypeEpic
	closedEpics, err := s.SearchIssues(ctx, "", types.IssueFilter{Status: &closed, IssueType: &epic})
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
