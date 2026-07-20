package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/query"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

var queryCmd = &cobra.Command{
	Use:     "query [expression]",
	GroupID: "issues",
	Short:   "Query issues using a simple query language",
	Long: `Query issues using a simple query language that supports compound filters,
boolean operators, and date-relative expressions.

The query language enables complex filtering that would otherwise require
multiple flags or piping through jq.

Syntax:
  field=value       Equality comparison
  field!=value      Inequality comparison
  field>value       Greater than
  field>=value      Greater than or equal
  field<value       Less than
  field<=value      Less than or equal

Boolean operators (case-insensitive):
  expr AND expr     Both conditions must match
  expr OR expr      Either condition can match
  NOT expr          Negates the condition
  (expr)            Grouping with parentheses

Supported fields:
  status            Stored status (open, in_progress, blocked, deferred, closed). Note: dependency-blocked issues stay "open"; use 'bd blocked' to find them
  priority          Priority level (0-4)
  type              Issue type (task, bug, feature, chore, epic, decision, spike, story, milestone; plus configured custom types). Invalid types are rejected, not silently empty
  assignee          Assigned user, e.g. rig/crew/name (use "none" for unassigned)
  owner             Issue owner, e.g. rig/crew/name or user@host
  label             Issue label (use "none" for unlabeled)
  title             Search in title (contains)
  description       Search in description (contains, "none" for empty)
  notes             Search in notes (contains)
  created           Creation date/time
  updated           Last update date/time
  started           Date/time issue first transitioned to in_progress
  closed            Close date/time
  id                Issue ID (supports wildcards: bd-*)
  spec              Spec ID (supports wildcards)
  pinned            Boolean (true/false)
  ephemeral         Boolean (true/false)
  template          Boolean (true/false)
  parent            Parent issue ID
  mol_type          Molecule type (swarm, patrol, work)

Values:
  Bare values may contain letters, digits, and _ - . : / (so Gas Town
  addresses like beads/crew/beads_eng_5 parse unquoted), plus a trailing *
  for the id/spec wildcard forms above (id=bd-*). Wrap a value in double
  quotes if it contains spaces or any other special character,
  e.g. owner="user@host" or title="hello world".

Date values:
  Relative durations: 7d (7 days ago), 24h (24 hours ago), 2w (2 weeks ago)
  Absolute dates: 2025-01-15, 2025-01-15T10:00:00Z
  Natural language: tomorrow, "next monday", "in 3 days"

Examples:
  bd query "status=open AND priority>1"
  bd query "status=open AND priority<=2 AND updated>7d"
  bd query "(status=open OR status=blocked) AND priority<2"
  bd query "type=bug AND label=urgent"
  bd query "NOT status=closed"
  bd query "assignee=none AND type=task"
  bd query "assignee=beads/crew/beads_eng_5 AND status=open"
  bd query "owner=beads/crew/beads_sr_pm"
  bd query "created>30d AND status!=closed"
  bd query "label=frontend OR label=backend"
  bd query "title=authentication AND priority=0"`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("query")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if usesProxiedServer() {
			return runQueryProxiedServer(cmd, rootCtx, args)
		}

		if len(args) == 0 {
			// Under --json, emit a JSON error object and do NOT dump help text
			// to stdout (beads-h2d5): cmd.Help() writes the usage banner to
			// stdout, which would pollute/break the --json contract.
			if jsonOutput {
				return HandleErrorRespectJSON("query expression is required")
			}
			fmt.Fprintf(os.Stderr, "Error: query expression is required\n\n")
			if err := cmd.Help(); err != nil {
				fmt.Fprintf(os.Stderr, "Error displaying help: %v\n", err)
			}
			return SilentExit()
		}

		queryStr := strings.Join(args, " ")

		limit, _ := cmd.Flags().GetInt("limit")
		// Reject a negative --limit up front (beads-eqi4): the SQL builders
		// only apply filter.Limit when >0, so a negative value silently returns
		// the full set. Shared with bd list (uh4i) via validateLimitFromCmd.
		if err := validateLimitFromCmd(cmd); err != nil {
			return err
		}
		allFlag, _ := cmd.Flags().GetBool("all")
		longFormat, _ := cmd.Flags().GetBool("long")
		sortBy, _ := cmd.Flags().GetString("sort")
		// Reject an invalid --sort field instead of silently falling back to
		// priority order (beads-a9rk): the SQL builder (OrderByForColumns →
		// SortDefs[""]) and the client sortIssues both default an unknown key to
		// priority with no error, a misleading false-green. bd list already
		// fails loud; route query through the same shared check.
		if err := validateSortField(sortBy); err != nil {
			return err
		}
		reverse, _ := cmd.Flags().GetBool("reverse")
		parseOnly, _ := cmd.Flags().GetBool("parse-only")
		offset, _ := cmd.Flags().GetInt("offset")
		if offset < 0 {
			return HandleErrorRespectJSON("--offset must be non-negative")
		}
		if offset > 0 {
			return HandleErrorRespectJSON("--offset is only supported under --proxied-server")
		}

		node, err := query.Parse(queryStr)
		if err != nil {
			return HandleErrorRespectJSON("parsing query: %v", err)
		}

		if parseOnly {
			fmt.Printf("Parsed query: %s\n", node.String())
			return nil
		}

		// Load configured custom types so a query against a valid custom type
		// is validated, not rejected. Best-effort: an unavailable store or a
		// load error falls back to built-in types only (beads-shux).
		var customTypes []string
		if store != nil {
			if ct, ctErr := store.GetCustomTypes(rootCtx); ctErr == nil {
				customTypes = ct
			}
		}

		eval := query.NewEvaluatorWithCustomTypes(time.Now(), customTypes)
		result, err := eval.Evaluate(node)
		if err != nil {
			return HandleErrorRespectJSON("evaluating query: %v", err)
		}

		// beads-o6lhl: exempt --sort id from the SQL LIMIT push, mirroring the
		// list-path guard (list_input.go: `if sortBy=="id" { sqlLimit=0 }`).
		// Bare "id" is NOT pushed into SQL ORDER BY (L182 below, intentional per
		// beads-l4ja), so pushing the LIMIT would truncate in DEFAULT PRIORITY
		// order BEFORE the client-side id sort ran — returning the wrong top-N
		// window (the priority-first rows re-sorted by id, not the true id-first
		// rows). Leaving Limit unset fetches the full set; sortIssues by id then
		// truncates to `limit` in Go below (as the predicate path already does).
		if limit > 0 && !result.RequiresPredicate && sortBy != "id" {
			result.Filter.Limit = limit
		}

		// Non-predicate path: push the requested sort into SQL so the LIMIT
		// window is selected in the sorted order, not the default priority
		// order (beads-s4sn). Without this, `--sort created --limit N` returned
		// the N highest-priority rows merely displayed in created order, not the
		// N newest. Bare "id" is Go-side only (empty SQL ORDER BY, beads-l4ja),
		// left to the client-side sortIssues below. The predicate path already
		// pages ALL candidates then sorts the full set (beads-7hu4), so it does
		// not rely on the SQL order.
		if !result.RequiresPredicate && sortBy != "" && sortBy != "id" {
			result.Filter.SortBy = sortBy
			result.Filter.SortDesc = reverse
		}

		if !allFlag && result.Filter.Status == nil && !hasExplicitStatusFilter(node) {
			result.Filter.ExcludeStatus = append(result.Filter.ExcludeStatus, types.StatusClosed)
		}

		ctx := rootCtx

		if store == nil {
			return HandleErrorRespectJSON("no storage available")
		}

		searchFilter := result.Filter

		// beads-ebpo: --limit (default 50) truncates the result set, but the human
		// header read len(issues) as the true match total — a plain `bd query`
		// over >50 matches printed "Found 50 issues" while more existed, with no
		// truncation signal. On the SQL path (non-predicate) fetch one extra row
		// to detect truncation, mirroring the `bd list` convention (list.go:43-44);
		// on the predicate path the full survivor set is already collected, so a
		// pre-trim length > limit is the truncation signal. Same display class as
		// beads-l39v/beads-phmp/beads-4wn0.
		effectiveLimit := limit
		if jsonOutput {
			var iwc []*types.IssueWithCounts
			truncated := false
			if result.RequiresPredicate && result.Predicate != nil {
				// Predicate path: page through ALL candidates (beads-7hu4) so a
				// selective predicate over a large table does not silently
				// under-return, then sort the full survivor set and truncate to
				// the limit so the result is the true sorted-top-N.
				iwc, err = collectPredicateMatchesWithCounts(ctx, store, searchFilter, sortBy, reverse, result.Predicate)
				if err != nil {
					return HandleErrorRespectJSON("%v", err)
				}
				sortIssuesWithCounts(iwc, sortBy, reverse)
				if limit > 0 && len(iwc) > limit {
					truncated = true
					iwc = iwc[:limit]
				}
			} else {
				// beads-o6lhl: same id-exemption as the predicate-limit guard
				// above — for --sort id, do NOT push the SQL limit (bare id has
				// no SQL ORDER BY, so a limit here truncates in priority order
				// before the client id-sort). Fetch all, id-sort, then truncate
				// in Go below (len>limit still signals truncation correctly).
				if limit > 0 && sortBy != "id" {
					searchFilter.Limit = limit + 1
				}
				iwc, err = store.SearchIssuesWithCounts(ctx, "", searchFilter)
				if err != nil {
					return HandleErrorRespectJSON("%v", err)
				}
				sortIssuesWithCounts(iwc, sortBy, reverse)
				if limit > 0 && len(iwc) > limit {
					truncated = true
					iwc = iwc[:limit]
				}
			}
			if iwc == nil {
				iwc = []*types.IssueWithCounts{}
			}
			if err := outputJSON(iwc); err != nil {
				return err
			}
			// beads-it9n7: JSON path uses the non-terminal-gated warn so a piped
			// consumer still learns the result was truncated (matches bd
			// list/search/ready). printTruncationHint here would be terminal-gated
			// and silently drop the signal under a pipe.
			printJSONTruncationWarn(truncated, effectiveLimit)
			return nil
		}

		var issues []*types.Issue
		truncated := false
		if result.RequiresPredicate && result.Predicate != nil {
			issues, err = collectPredicateMatches(ctx, store, searchFilter, sortBy, reverse, result.Predicate)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			sortIssues(issues, sortBy, reverse)
			if limit > 0 && len(issues) > limit {
				truncated = true
				issues = issues[:limit]
			}
		} else {
			// beads-o6lhl: text leg of the same id-exemption (see JSON leg
			// above) — --sort id must fetch all, id-sort, then truncate in Go,
			// not push the SQL limit in the un-ordered priority default.
			if limit > 0 && sortBy != "id" {
				searchFilter.Limit = limit + 1
			}
			issues, err = store.SearchIssues(ctx, "", searchFilter)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			sortIssues(issues, sortBy, reverse)
			if limit > 0 && len(issues) > limit {
				truncated = true
				issues = issues[:limit]
			}
		}

		outputQueryResults(issues, queryStr, longFormat, truncated, effectiveLimit)
		printTruncationHint(truncated, effectiveLimit)
		return nil
	},
}

// hasExplicitStatusFilter reports whether the query carries explicit
// intent-to-include-closed, which disables the default closed-exclusion at the
// call site (query.go:186). A `status` comparison is the direct signal.
//
// beads-6dgb3: a bare closed-date predicate (`closed>7d`, `closed=DATE`, the
// `closed_at` alias) also counts. The closed date field is set ONLY on closed
// issues (closed_at non-NULL <-> status=closed exactly), so default-excluding
// closed rows strips the exact rows such a predicate could match — the query
// self-nullifies to empty. A query naming `closed` is definitionally about
// closed issues, so excluding them is a contradiction (same "explicit user
// intent yields to default-exclude" principle as SWEEP-124). SCOPE: only the
// closed date field self-nullifies — started/updated/created rows survive
// default-exclusion and carry their timestamps, so they are NOT included here.
func hasExplicitStatusFilter(node query.Node) bool {
	switch n := node.(type) {
	case *query.ComparisonNode:
		return n.Field == "status" || n.Field == "closed" || n.Field == "closed_at"
	case *query.AndNode:
		return hasExplicitStatusFilter(n.Left) || hasExplicitStatusFilter(n.Right)
	case *query.OrNode:
		return hasExplicitStatusFilter(n.Left) || hasExplicitStatusFilter(n.Right)
	case *query.NotNode:
		return hasExplicitStatusFilter(n.Operand)
	default:
		return false
	}
}

// outputQueryResults formats and displays query results. When truncated, the
// header is qualified so the shown page size is not read as the true match
// total (beads-ebpo); effectiveLimit is the applied --limit.
func outputQueryResults(issues []*types.Issue, queryStr string, longFormat, truncated bool, effectiveLimit int) {
	if len(issues) == 0 {
		fmt.Printf("No issues found matching query: %s\n", queryStr)
		return
	}

	header := fmt.Sprintf("Found %d issues:", len(issues))
	if truncated {
		header = fmt.Sprintf("Showing %d issues (limit %d — more matched):", len(issues), effectiveLimit)
	}

	if longFormat {
		fmt.Printf("\n%s\n\n", header)
		for _, issue := range issues {
			fmt.Printf("%s [P%d] [%s] %s\n", issue.ID, issue.Priority, issue.IssueType, issue.Status)
			// beads-asjzp: sanitize the title for terminal display (7n9y sink
			// slice) — an untrusted imported title can carry OSC/CSI escapes.
			fmt.Printf("  %s\n", displayTitle(issue.Title))
			if issue.Assignee != "" {
				fmt.Printf("  Assignee: %s\n", ui.SanitizeForTerminal(issue.Assignee))
			}
			if len(issue.Labels) > 0 {
				fmt.Printf("  Labels: %v\n", displayLabels(issue.Labels))
			}
			fmt.Println()
		}
	} else {
		// Use same compact format as list command
		fmt.Printf("%s\n", header)
		var buf strings.Builder
		for _, issue := range issues {
			formatQueryIssue(&buf, issue)
		}
		fmt.Print(buf.String())
	}
}

// formatQueryIssue formats a single issue in compact format
func formatQueryIssue(buf *strings.Builder, issue *types.Issue) {
	labelsStr := ""
	if len(issue.Labels) > 0 {
		labelsStr = fmt.Sprintf(" %v", displayLabels(issue.Labels))
	}
	assigneeStr := ""
	if issue.Assignee != "" {
		assigneeStr = fmt.Sprintf(" @%s", ui.SanitizeForTerminal(issue.Assignee))
	}

	// Get styled status icon
	statusIcon := ui.RenderStatusIcon(string(issue.Status))

	if issue.Status == types.StatusClosed {
		line := fmt.Sprintf("%s %s [P%d] [%s]%s%s - %s",
			statusIcon, issue.ID, issue.Priority,
			issue.IssueType, assigneeStr, labelsStr, displayTitle(issue.Title))
		buf.WriteString(ui.RenderClosedLine(line))
		buf.WriteString("\n")
	} else {
		buf.WriteString(fmt.Sprintf("%s %s [%s] [%s]%s%s - %s\n",
			statusIcon,
			ui.RenderID(issue.ID),
			ui.RenderPriority(issue.Priority),
			ui.RenderType(string(issue.IssueType)),
			assigneeStr, labelsStr, displayTitle(issue.Title)))
	}
}

func init() {
	queryCmd.Flags().IntP("limit", "n", 50, "Limit results (default: 50, 0 = unlimited)")
	queryCmd.Flags().Int("offset", 0, "Skip the first N matching results (0-based). Only supported under --proxied-server.")
	queryCmd.Flags().BoolP("all", "a", false, "Include closed issues (default: exclude closed)")
	queryCmd.Flags().Bool("long", false, "Show detailed multi-line output for each issue")
	queryCmd.Flags().String("sort", "", "Sort by field: priority, created, updated, closed, status, id, title, type, assignee")
	queryCmd.Flags().BoolP("reverse", "r", false, "Reverse sort order")
	queryCmd.Flags().Bool("parse-only", false, "Only parse the query and show the AST (for debugging)")

	rootCmd.AddCommand(queryCmd)
}
