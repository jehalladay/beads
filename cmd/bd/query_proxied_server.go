package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/query"
	"github.com/steveyegge/beads/internal/types"
)

func runQueryProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Error: query expression is required\n\n")
		if err := cmd.Help(); err != nil {
			fmt.Fprintf(os.Stderr, "Error displaying help: %v\n", err)
		}
		return SilentExit()
	}

	queryStr := strings.Join(args, " ")

	limit, _ := cmd.Flags().GetInt("limit")
	allFlag, _ := cmd.Flags().GetBool("all")
	longFormat, _ := cmd.Flags().GetBool("long")
	sortBy, _ := cmd.Flags().GetString("sort")
	// Reject an invalid --sort field (beads-a9rk), mirroring the direct query
	// path + bd list; an unknown key otherwise silently falls back to priority.
	if err := validateSortField(sortBy); err != nil {
		return err
	}
	reverse, _ := cmd.Flags().GetBool("reverse")
	parseOnly, _ := cmd.Flags().GetBool("parse-only")
	offset, _ := cmd.Flags().GetInt("offset")
	if offset < 0 {
		return HandleErrorRespectJSON("--offset must be non-negative")
	}
	// Reject a negative --limit (beads-eqi4): filter.Limit is applied only when
	// >0, so a negative value silently returns the full set. Mirrors the direct
	// query path + bd list (uh4i).
	if err := validateLimitFromCmd(cmd); err != nil {
		return err
	}

	node, err := query.Parse(queryStr)
	if err != nil {
		return HandleErrorRespectJSON("parsing query: %v", err)
	}

	if parseOnly {
		fmt.Printf("Parsed query: %s\n", node.String())
		return nil
	}

	uw, err := openProxiedListUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)

	// Load configured custom types so a query against a valid custom type is
	// validated, not rejected. Best-effort: a load error falls back to
	// built-in types only (beads-shux).
	var customTypes []string
	if ct, ctErr := uw.ConfigUseCase().GetCustomTypes(ctx); ctErr == nil {
		customTypes = ct
	}

	eval := query.NewEvaluatorWithCustomTypes(time.Now(), customTypes)
	result, err := eval.Evaluate(node)
	if err != nil {
		return HandleErrorRespectJSON("evaluating query: %v", err)
	}

	if result.RequiresPredicate && offset > 0 {
		return HandleErrorRespectJSON("--offset is not supported with OR/predicate queries")
	}

	if limit > 0 && !result.RequiresPredicate {
		result.Filter.Limit = limit
	}

	if !allFlag && result.Filter.Status == nil && !hasExplicitStatusFilter(node) {
		result.Filter.ExcludeStatus = append(result.Filter.ExcludeStatus, types.StatusClosed)
	}

	if offset > 0 && sortBy != "" {
		return HandleErrorRespectJSON("--offset is not supported with --sort (query applies --sort client-side, which cannot be paginated)")
	}

	searchFilter := result.Filter
	searchFilter.Offset = offset

	// beads-222x / beads-s4sn: push the sort into the SQL query on the
	// non-predicate path so the LIMIT window is selected in the requested
	// order, not default priority order then re-sorted (which returns the
	// wrong N rows). Mirrors the direct path (cmd/bd/query.go). --sort id is
	// excluded (natural-numeric ID compare can't be an SQL ORDER BY, so it
	// stays client-sorted); the offset+sort combo is already rejected above.
	if !result.RequiresPredicate && sortBy != "" && sortBy != "id" {
		searchFilter.SortBy = sortBy
		searchFilter.SortDesc = reverse
	}

	if jsonOutput {
		var iwc []*types.IssueWithCounts
		var truncated bool
		if result.RequiresPredicate && result.Predicate != nil {
			// Predicate path: page through ALL candidates (beads-j1sh) so a
			// selective predicate does not silently under-return, and derive the
			// truncation hint from TRUE overflow rather than a single window.
			res, err := collectProxiedPredicateMatchesWithCounts(ctx, uw.IssueUseCase(), searchFilter, sortBy, reverse, result.Predicate)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			iwc = res.items
			sortIssuesWithCounts(iwc, sortBy, reverse)
			truncated = res.capReached || (limit > 0 && len(iwc) > limit)
			if limit > 0 && len(iwc) > limit {
				iwc = iwc[:limit]
			}
		} else {
			page, err := uw.IssueUseCase().SearchIssuesWithCounts(ctx, "", searchFilter)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			iwc = page.Items
			truncated = page.HasMore
			sortIssuesWithCounts(iwc, sortBy, reverse)
		}
		if iwc == nil {
			iwc = []*types.IssueWithCounts{}
		}
		if err := outputJSON(iwc); err != nil {
			return err
		}
		printTruncationHint(truncated, limit)
		return nil
	}

	var issues []*types.Issue
	var truncated bool
	if result.RequiresPredicate && result.Predicate != nil {
		res, err := collectProxiedPredicateMatches(ctx, uw.IssueUseCase(), searchFilter, sortBy, reverse, result.Predicate)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		issues = res.items
		sortIssues(issues, sortBy, reverse)
		truncated = res.capReached || (limit > 0 && len(issues) > limit)
		if limit > 0 && len(issues) > limit {
			issues = issues[:limit]
		}
	} else {
		page, err := uw.IssueUseCase().SearchIssues(ctx, "", searchFilter)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		issues = page.Items
		truncated = page.HasMore
		sortIssues(issues, sortBy, reverse)
	}

	outputQueryResults(issues, queryStr, longFormat)
	printTruncationHint(truncated, limit)
	return nil
}
