package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/steveyegge/beads/internal/validation"
)

var searchCmd = &cobra.Command{
	Use:     "search [query]",
	GroupID: "issues",
	Short:   "Search issues by text query",
	Long: `Search issues across title and ID (excludes closed issues by default).

ID-like queries (e.g., "bd-123", "hq-319") use fast exact/prefix matching.
Text queries search titles. Use --desc-contains for description search.
Use --status all to include closed issues.

Examples:
  bd search "authentication bug"
  bd search "login" --status open
  bd search "database" --label backend --limit 10
  bd search --query "performance" --assignee alice
  bd search "bd-5q" # Search by partial ID (fast prefix match)
  bd search "security" --priority-min 0 --priority-max 2
  bd search "bug" --created-after 2025-01-01
  bd search "refactor" --status all  # Include closed issues
  bd search "bug" --sort priority
  bd search "task" --sort created --reverse
  bd search "api" --desc-contains "endpoint"
  bd search "cleanup" --no-assignee --no-labels`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("search")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		// beads-iq3i: route to the proxied-server handler when the global
		// store is nil (proxied-server mode: main.go PersistentPreRun returns
		// before newDoltStore). The direct body below dereferences the nil
		// store (SearchIssues/GetLabelsForIssues/…) and panics on hub crew.
		if usesProxiedServer() {
			return runSearchProxiedServer(cmd, rootCtx, args)
		}
		return runSearchDirect(cmd, args)
	},
}

// runSearchDirect is the embedded/direct-store search path.
func runSearchDirect(cmd *cobra.Command, args []string) error {
	query, err := parseSearchQuery(cmd, args)
	if err != nil {
		return err
	}

	ctx := rootCtx

	// beads-deud: custom statuses/types come from store config (nil-safe).
	cfg, cfgErr := loadDirectListFilterConfig(ctx, store)
	if cfgErr != nil {
		return HandleErrorRespectJSON("%v", cfgErr)
	}
	params, err := parseSearchParams(cmd, cfg)
	if err != nil {
		return err
	}

	issues, err := store.SearchIssues(ctx, query, params.filter)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	sortIssues(issues, params.sortBy, params.reverse)

	if jsonOutput {
		issueIDs := searchIssueIDs(issues)
		labelsMap, err := store.GetLabelsForIssues(ctx, issueIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get labels: %v\n", err)
			labelsMap = make(map[string][]string)
		}
		depCounts, err := store.GetDependencyCounts(ctx, issueIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get dependency counts: %v\n", err)
			depCounts = make(map[string]*types.DependencyCounts)
		}
		commentCounts, err := store.GetCommentCounts(ctx, issueIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get comment counts: %v\n", err)
			commentCounts = make(map[string]int)
		}
		return outputJSON(buildSearchIssuesWithCounts(issues, labelsMap, depCounts, commentCounts))
	}

	labelsMap, _ := store.GetLabelsForIssues(ctx, searchIssueIDs(issues))
	for _, issue := range issues {
		issue.Labels = labelsMap[issue.ID]
	}
	// beads-4wn0: report the TRUE match count in the header, not the
	// --limit-truncated page size. When the returned page fills the limit,
	// re-query with Limit=0 to learn the real total (mirrors bd ready's
	// truncation-detection at ready.go). Otherwise `bd search <common> --limit
	// N` (and a plain search >50 matches, since --limit defaults to 50)
	// reported "Found N" == page size, undercounting silently.
	totalMatches := len(issues)
	if params.filter.Limit > 0 && len(issues) == params.filter.Limit {
		countFilter := params.filter
		countFilter.Limit = 0
		if allIssues, cErr := store.SearchIssues(ctx, query, countFilter); cErr == nil && len(allIssues) > len(issues) {
			totalMatches = len(allIssues)
		}
	}
	outputSearchResults(issues, query, params.longFormat, totalMatches)
	return nil
}

// searchParams holds the validated flags shared by the direct and
// proxied-server search paths (beads-iq3i).
type searchParams struct {
	filter     types.IssueFilter
	sortBy     string
	reverse    bool
	longFormat bool
}

// parseSearchQuery resolves the search query from positional args or --query,
// printing help + a hard error when empty (parity across both backends).
func parseSearchQuery(cmd *cobra.Command, args []string) (string, error) {
	queryFlag, _ := cmd.Flags().GetString("query")
	var query string
	if len(args) > 0 {
		query = strings.Join(args, " ")
	} else if queryFlag != "" {
		query = queryFlag
	}
	if query == "" {
		if err := cmd.Help(); err != nil {
			fmt.Fprintf(os.Stderr, "Error displaying help: %v\n", err)
		}
		return "", HandleErrorRespectJSON("search query is required")
	}
	return query, nil
}

// searchIssueIDs extracts the IDs of a result set for the enrichment lookups.
func searchIssueIDs(issues []*types.Issue) []string {
	ids := make([]string, len(issues))
	for i, issue := range issues {
		ids[i] = issue.ID
	}
	return ids
}

// buildSearchIssuesWithCounts assembles the --json payload from the enrichment
// maps, shared by both backends so the JSON shape stays identical.
func buildSearchIssuesWithCounts(issues []*types.Issue, labelsMap map[string][]string, depCounts map[string]*types.DependencyCounts, commentCounts map[string]int) []*types.IssueWithCounts {
	for _, issue := range issues {
		issue.Labels = labelsMap[issue.ID]
	}
	out := make([]*types.IssueWithCounts, len(issues))
	for i, issue := range issues {
		counts := depCounts[issue.ID]
		if counts == nil {
			counts = &types.DependencyCounts{DependencyCount: 0, DependentCount: 0}
		}
		out[i] = &types.IssueWithCounts{
			Issue:           issue,
			DependencyCount: counts.DependencyCount,
			DependentCount:  counts.DependentCount,
			CommentCount:    commentCounts[issue.ID],
		}
	}
	return out
}

// parseSearchParams validates the filter flags and builds the IssueFilter.
// cfg carries the custom statuses/types (nil-safe on the direct path) so both
// backends share identical validation.
func parseSearchParams(cmd *cobra.Command, cfg listFilterConfig) (searchParams, error) {
	// Get filter flags
	status, _ := cmd.Flags().GetString("status")
	assignee, _ := cmd.Flags().GetString("assignee")
	issueType, _ := cmd.Flags().GetString("type")
	limit, _ := cmd.Flags().GetInt("limit")
	// Reject a negative --limit up front (beads-eqi4): the SQL builders
	// only apply filter.Limit when >0, so a negative value silently returns
	// the full set. Shared with bd list (uh4i) via validateLimitFromCmd.
	if err := validateLimitFromCmd(cmd); err != nil {
		return searchParams{}, err
	}
	labels, _ := cmd.Flags().GetStringSlice("label")
	labelsAny, _ := cmd.Flags().GetStringSlice("label-any")
	longFormat, _ := cmd.Flags().GetBool("long")
	sortBy, _ := cmd.Flags().GetString("sort")
	reverse, _ := cmd.Flags().GetBool("reverse")

	// beads-y04n: validate --sort against the shared key set bd list/query
	// enforce. Without this, an invalid --sort silently fell back to the
	// default priority sort (sqlbuild.OrderByForColumns → SortDefs[""], and
	// the client sortIssues default) instead of failing loud like bd list —
	// a misleading false-green where the user believes results are sorted by
	// their (typo'd) field. Route search through the same shared check
	// (beads-a9rk helper).
	if err := validateSortField(sortBy); err != nil {
		return searchParams{}, err
	}

	// Date range flags
	createdAfter, _ := cmd.Flags().GetString("created-after")
	createdBefore, _ := cmd.Flags().GetString("created-before")
	updatedAfter, _ := cmd.Flags().GetString("updated-after")
	updatedBefore, _ := cmd.Flags().GetString("updated-before")
	closedAfter, _ := cmd.Flags().GetString("closed-after")
	closedBefore, _ := cmd.Flags().GetString("closed-before")

	// Priority range flags
	priorityMinStr, _ := cmd.Flags().GetString("priority-min")
	priorityMaxStr, _ := cmd.Flags().GetString("priority-max")

	// Pattern matching flags
	descContains, _ := cmd.Flags().GetString("desc-contains")
	notesContains, _ := cmd.Flags().GetString("notes-contains")
	externalContains, _ := cmd.Flags().GetString("external-contains")

	// Empty/null check flags
	emptyDesc, _ := cmd.Flags().GetBool("empty-description")
	noAssignee, _ := cmd.Flags().GetBool("no-assignee")
	noLabels, _ := cmd.Flags().GetBool("no-labels")

	// Normalize labels
	labels = utils.NormalizeLabels(labels)
	labelsAny = utils.NormalizeLabels(labelsAny)

	// Build filter
	filter := types.IssueFilter{
		Limit: limit,
	}

	// Push the requested sort into SQL so the LIMIT window is selected in
	// the sorted order, not the default priority order (beads-s4sn). Without
	// this, `--sort created --limit N` returned the N highest-priority rows
	// merely displayed in created order, not the N newest. Bare "id" is
	// Go-side only (empty SQL ORDER BY, see beads-l4ja), so leave it to the
	// client-side sortIssues below.
	if sortBy != "" && sortBy != "id" {
		filter.SortBy = sortBy
		filter.SortDesc = reverse
	}

	// beads-deud: validate --status/--type against their documented enums,
	// mirroring bd list, so an invalid value is a hard error (rc!=0) instead
	// of a silent false-empty result. Custom sets come from cfg (nil-safe on
	// the direct path; from the UOW on proxied) so custom statuses + infra
	// types still pass. (search's --priority-min/max already validate via
	// validation.ValidatePriority.)
	if status != "" && status != "all" {
		// beads-ybc7: support comma-multi (OR) status like bd list
		// (list_filter.go). A single value keeps the scalar filter.Status;
		// two+ comma-separated values validate each and build filter.Statuses
		// (an IN filter honored by the shared sqlbuild path). Without this,
		// `bd search --status open,closed` failed (post-deud) as an invalid
		// single status rather than matching both, breaking parity with bd list.
		names := cfg.customStatusNames()
		statusParts := strings.Split(status, ",")
		if len(statusParts) == 1 {
			s := types.Status(strings.TrimSpace(statusParts[0])).Normalize()
			if !s.IsValidWithCustom(names) {
				return searchParams{}, HandleErrorRespectJSON("invalid status %q (valid: %s)", status, validStatusList(names))
			}
			filter.Status = &s
		} else {
			for _, part := range statusParts {
				s := types.Status(strings.TrimSpace(part)).Normalize()
				if !s.IsValidWithCustom(names) {
					return searchParams{}, HandleErrorRespectJSON("invalid status %q in multi-status filter (valid: %s)", strings.TrimSpace(part), validStatusList(names))
				}
				filter.Statuses = append(filter.Statuses, s)
			}
		}
	} else if status != "all" {
		// Default: exclude closed issues to reduce scan scope (hq-319).
		// With 12K+ issues, ~60-70% are closed — excluding them lets the
		// query use the status index to skip the majority of rows.
		// Use --status all to search everything including closed.
		filter.ExcludeStatus = []types.Status{types.StatusClosed}
	}

	// beads-sabd: trim read-side assignee (write side trims via llzt
	// @7f1b7dae5; read never trimmed -> padded value silently matched nothing).
	if a := strings.TrimSpace(assignee); a != "" {
		filter.Assignee = &a
	}

	if issueType != "" {
		t := issueTypeFilterValue(issueType)
		if !t.IsValidWithCustom(cfg.customTypes) {
			validTypes := "bug, feature, task, epic, chore, decision"
			if len(cfg.customTypes) > 0 {
				validTypes += ", " + strings.Join(cfg.customTypes, ", ")
			}
			return searchParams{}, HandleErrorRespectJSON("invalid issue type %q (valid: %s)", issueType, validTypes)
		}
		filter.IssueType = &t
	}

	if len(labels) > 0 {
		filter.Labels = labels
	}

	if len(labelsAny) > 0 {
		filter.LabelsAny = labelsAny
	}

	// Pattern matching
	if descContains != "" {
		filter.DescriptionContains = descContains
	}
	if notesContains != "" {
		filter.NotesContains = notesContains
	}
	if externalContains != "" {
		filter.ExternalRefContains = externalContains
	}

	// Empty/null checks
	if emptyDesc {
		filter.EmptyDescription = true
	}
	if noAssignee {
		filter.NoAssignee = true
	}
	if noLabels {
		filter.NoLabels = true
	}

	// Date ranges
	if createdAfter != "" {
		t, err := parseTimeFlag(createdAfter)
		if err != nil {
			return searchParams{}, HandleErrorRespectJSON("parsing --created-after: %v", err)
		}
		filter.CreatedAfter = &t
	}
	if createdBefore != "" {
		t, err := parseTimeFlag(createdBefore)
		if err != nil {
			return searchParams{}, HandleErrorRespectJSON("parsing --created-before: %v", err)
		}
		filter.CreatedBefore = &t
	}
	if updatedAfter != "" {
		t, err := parseTimeFlag(updatedAfter)
		if err != nil {
			return searchParams{}, HandleErrorRespectJSON("parsing --updated-after: %v", err)
		}
		filter.UpdatedAfter = &t
	}
	if updatedBefore != "" {
		t, err := parseTimeFlag(updatedBefore)
		if err != nil {
			return searchParams{}, HandleErrorRespectJSON("parsing --updated-before: %v", err)
		}
		filter.UpdatedBefore = &t
	}
	if closedAfter != "" {
		t, err := parseTimeFlag(closedAfter)
		if err != nil {
			return searchParams{}, HandleErrorRespectJSON("parsing --closed-after: %v", err)
		}
		filter.ClosedAfter = &t
	}
	if closedBefore != "" {
		t, err := parseTimeFlag(closedBefore)
		if err != nil {
			return searchParams{}, HandleErrorRespectJSON("parsing --closed-before: %v", err)
		}
		filter.ClosedBefore = &t
	}

	if cmd.Flags().Changed("priority-min") {
		priorityMin, err := validation.ValidatePriority(priorityMinStr)
		if err != nil {
			return searchParams{}, HandleErrorRespectJSON("parsing --priority-min: %v", err)
		}
		filter.PriorityMin = &priorityMin
	}
	if cmd.Flags().Changed("priority-max") {
		priorityMax, err := validation.ValidatePriority(priorityMaxStr)
		if err != nil {
			return searchParams{}, HandleErrorRespectJSON("parsing --priority-max: %v", err)
		}
		filter.PriorityMax = &priorityMax
	}

	metadataFieldFlags, _ := cmd.Flags().GetStringArray("metadata-field")
	if len(metadataFieldFlags) > 0 {
		filter.MetadataFields = make(map[string]string, len(metadataFieldFlags))
		for _, mf := range metadataFieldFlags {
			k, v, ok := strings.Cut(mf, "=")
			if !ok || k == "" {
				return searchParams{}, HandleErrorRespectJSON("invalid --metadata-field: expected key=value, got %q", mf)
			}
			if err := storage.ValidateMetadataKey(k); err != nil {
				return searchParams{}, HandleErrorRespectJSON("invalid --metadata-field key: %v", err)
			}
			filter.MetadataFields[k] = v
		}
	}
	hasMetadataKey, _ := cmd.Flags().GetString("has-metadata-key")
	if hasMetadataKey != "" {
		if err := storage.ValidateMetadataKey(hasMetadataKey); err != nil {
			return searchParams{}, HandleErrorRespectJSON("invalid --has-metadata-key: %v", err)
		}
		filter.HasMetadataKey = hasMetadataKey
	}

	return searchParams{
		filter:     filter,
		sortBy:     sortBy,
		reverse:    reverse,
		longFormat: longFormat,
	}, nil
}

// outputSearchResults formats and displays search results. totalMatches is the
// true number of matching issues (>= len(issues) when the view is
// --limit-truncated); the header reports it, and a "Showing K of N" line is
// added when the shown page is truncated (beads-4wn0).
func outputSearchResults(issues []*types.Issue, query string, longFormat bool, totalMatches int) {
	if len(issues) == 0 {
		fmt.Printf("No issues found matching '%s'\n", query)
		return
	}
	if totalMatches < len(issues) {
		totalMatches = len(issues)
	}
	truncated := totalMatches > len(issues)

	if longFormat {
		// Long format: multi-line with details
		fmt.Printf("\nFound %d issues matching '%s':\n\n", totalMatches, query)
		for _, issue := range issues {
			fmt.Printf("%s [P%d] [%s] %s\n", issue.ID, issue.Priority, issue.IssueType, issue.Status)
			fmt.Printf("  %s\n", issue.Title)
			if issue.Assignee != "" {
				fmt.Printf("  Assignee: %s\n", issue.Assignee)
			}
			if len(issue.Labels) > 0 {
				fmt.Printf("  Labels: %v\n", issue.Labels)
			}
			fmt.Println()
		}
	} else {
		// Compact format: one line per issue
		fmt.Printf("Found %d issues matching '%s':\n", totalMatches, query)
		for _, issue := range issues {
			labelsStr := ""
			if len(issue.Labels) > 0 {
				labelsStr = fmt.Sprintf(" %v", issue.Labels)
			}
			assigneeStr := ""
			if issue.Assignee != "" {
				assigneeStr = fmt.Sprintf(" @%s", issue.Assignee)
			}
			fmt.Printf("%s [P%d] [%s] %s%s%s - %s\n",
				issue.ID, issue.Priority, issue.IssueType, issue.Status,
				assigneeStr, labelsStr, issue.Title)
		}
	}

	if truncated {
		fmt.Printf("\n%s\n", ui.RenderMuted(fmt.Sprintf("Showing %d of %d matching issues. Use -n to show more.", len(issues), totalMatches)))
	}
}

func init() {
	searchCmd.Flags().String("query", "", "Search query (alternative to positional argument)")
	searchCmd.Flags().StringP("status", "s", "", "Filter by stored status (open, in_progress, blocked, deferred, closed, all). Default excludes closed; use 'all' to include closed. Note: dependency-blocked issues use 'bd blocked'")
	searchCmd.Flags().StringP("assignee", "a", "", "Filter by assignee")
	searchCmd.Flags().StringP("type", "t", "", "Filter by type (bug, feature, task, epic, chore, decision, merge-request, molecule, gate)")
	searchCmd.Flags().StringSliceP("label", "l", []string{}, "Filter by labels (AND: must have ALL)")
	searchCmd.Flags().StringSlice("label-any", []string{}, "Filter by labels (OR: must have AT LEAST ONE)")
	searchCmd.Flags().IntP("limit", "n", 50, "Limit results (default: 50)")
	searchCmd.Flags().Bool("long", false, "Show detailed multi-line output for each issue")
	searchCmd.Flags().String("sort", "", "Sort by field: "+sortFieldsHelp)
	searchCmd.Flags().BoolP("reverse", "r", false, "Reverse sort order")

	// Date range flags
	searchCmd.Flags().String("created-after", "", "Filter issues created after date (YYYY-MM-DD or RFC3339)")
	searchCmd.Flags().String("created-before", "", "Filter issues created before date (YYYY-MM-DD or RFC3339)")
	searchCmd.Flags().String("updated-after", "", "Filter issues updated after date (YYYY-MM-DD or RFC3339)")
	searchCmd.Flags().String("updated-before", "", "Filter issues updated before date (YYYY-MM-DD or RFC3339)")
	searchCmd.Flags().String("closed-after", "", "Filter issues closed after date (YYYY-MM-DD or RFC3339)")
	searchCmd.Flags().String("closed-before", "", "Filter issues closed before date (YYYY-MM-DD or RFC3339)")

	// Priority range flags
	searchCmd.Flags().String("priority-min", "", "Filter by minimum priority (inclusive, 0-4 or P0-P4)")
	searchCmd.Flags().String("priority-max", "", "Filter by maximum priority (inclusive, 0-4 or P0-P4)")

	// Pattern matching flags
	searchCmd.Flags().String("desc-contains", "", "Filter by description substring (case-insensitive)")
	searchCmd.Flags().String("notes-contains", "", "Filter by notes substring (case-insensitive)")
	searchCmd.Flags().String("external-contains", "", "Filter by external ref substring (case-insensitive)")

	// Empty/null check flags
	searchCmd.Flags().Bool("empty-description", false, "Filter issues with empty or missing description")
	searchCmd.Flags().Bool("no-assignee", false, "Filter issues with no assignee")
	searchCmd.Flags().Bool("no-labels", false, "Filter issues with no labels")

	// Metadata filtering (GH#1406)
	searchCmd.Flags().StringArray("metadata-field", nil, "Filter by metadata field (key=value, repeatable)")
	searchCmd.Flags().String("has-metadata-key", "", "Filter issues that have this metadata key set")

	rootCmd.AddCommand(searchCmd)
}
