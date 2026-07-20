package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/steveyegge/beads/internal/validation"
)

var countCmd = &cobra.Command{
	Use:     "count",
	GroupID: "views",
	Short:   "Count issues matching filters",
	Long: `Count issues matching the specified filters.

By default, counts OPEN issues only (excluding closed/pinned), matching
'bd list's open-scope default. Use --status all for the true total, or
--status <state> for a specific state. Use --by-* flags to group counts by
different attributes.

Examples:
  bd count                          # Count OPEN issues (default excludes closed/pinned)
  bd count --status all             # Count ALL issues (including closed)
  bd count --status open            # Count open issues
  bd count --by-status              # Group count by status
  bd count --by-priority            # Group count by priority
  bd count --by-type                # Group count by issue type
  bd count --by-assignee            # Group count by assignee
  bd count --by-label               # Group count by label
  bd count --assignee alice --by-status  # Count alice's issues by status
  bd count --include-infra          # Count issues + wisps tier (matches 'bd list --include-infra --all' cardinality)
`,
	Args:          countArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("count")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		status, _ := cmd.Flags().GetString("status")
		assignee, _ := cmd.Flags().GetString("assignee")
		issueType, _ := cmd.Flags().GetString("type")
		labels, _ := cmd.Flags().GetStringSlice("label")
		labelsAny, _ := cmd.Flags().GetStringSlice("label-any")
		titleSearch, _ := cmd.Flags().GetString("title")
		idFilter, _ := cmd.Flags().GetString("id")

		// Pattern matching flags
		titleContains, _ := cmd.Flags().GetString("title-contains")
		descContains, _ := cmd.Flags().GetString("desc-contains")
		notesContains, _ := cmd.Flags().GetString("notes-contains")

		// Date range flags
		createdAfter, _ := cmd.Flags().GetString("created-after")
		createdBefore, _ := cmd.Flags().GetString("created-before")
		updatedAfter, _ := cmd.Flags().GetString("updated-after")
		updatedBefore, _ := cmd.Flags().GetString("updated-before")
		closedAfter, _ := cmd.Flags().GetString("closed-after")
		closedBefore, _ := cmd.Flags().GetString("closed-before")

		// Empty/null check flags
		emptyDesc, _ := cmd.Flags().GetBool("empty-description")
		noAssignee, _ := cmd.Flags().GetBool("no-assignee")
		noLabels, _ := cmd.Flags().GetBool("no-labels")

		// Group by flags
		byStatus, _ := cmd.Flags().GetBool("by-status")
		byPriority, _ := cmd.Flags().GetBool("by-priority")
		byType, _ := cmd.Flags().GetBool("by-type")
		byAssignee, _ := cmd.Flags().GetBool("by-assignee")
		byLabel, _ := cmd.Flags().GetBool("by-label")

		// Determine groupBy value
		groupBy := ""
		groupCount := 0
		if byStatus {
			groupBy = "status"
			groupCount++
		}
		if byPriority {
			groupBy = "priority"
			groupCount++
		}
		if byType {
			groupBy = "type"
			groupCount++
		}
		if byAssignee {
			groupBy = "assignee"
			groupCount++
		}
		if byLabel {
			groupBy = "label"
			groupCount++
		}

		if groupCount > 1 {
			return HandleErrorRespectJSON("only one --by-* flag can be specified")
		}

		// Normalize labels
		labels = utils.NormalizeLabels(labels)
		labelsAny = utils.NormalizeLabels(labelsAny)

		ctx := rootCtx

		// beads-2om1: in proxiedServerMode the global `store` is nil (main.go
		// PersistentPreRun returns before newDoltStore), so store.CountIssues /
		// CountIssuesByGroup / loadDirectListFilterConfig below would fail with
		// "storage is nil" for hub crew. Resolve a config source + count backend
		// once here — proxied via the UOW (CountIssues/CountIssuesByGroup added
		// to IssueUseCase), direct via `store` — and route the shared
		// filter-building through them. Interface-extension leg of the aocj/fszd
		// proxied read-handler family.
		cb := newCountBackend(ctx)
		defer cb.close()

		// beads-deud: validate --status/--type/--priority against their
		// documented enums, mirroring bd list (list_filter.go) so an invalid
		// value is a hard error (rc!=0) instead of a silent false-zero. The
		// custom-status/type sets come from the store's config (nil-safe), so
		// custom statuses + infra types (convoy/merge-request/...) still pass.
		filterCfg, cfgErr := cb.loadFilterConfig(ctx)
		if cfgErr != nil {
			return HandleErrorRespectJSON("%v", cfgErr)
		}

		// Direct mode
		filter := types.IssueFilter{}
		// beads-ybc7: support comma-multi (OR) status like bd list
		// (list_filter.go): a single value keeps the scalar filter.Status; two+
		// comma-separated values validate each and build filter.Statuses (an IN
		// filter honored by the shared sqlbuild path). Without this,
		// `bd count --status open,closed` failed (post-deud) as an invalid
		// single status rather than counting both, breaking parity with bd list.
		if status != "" && status != "all" {
			names := filterCfg.customStatusNames()
			statusParts := strings.Split(status, ",")
			if len(statusParts) == 1 {
				s := types.Status(strings.TrimSpace(statusParts[0])).Normalize()
				if !s.IsValidWithCustom(names) {
					return HandleErrorRespectJSON("invalid status %q (valid: %s)", status, validStatusList(names))
				}
				if s == types.StatusBlocked {
					// beads-7f3g: "blocked" is a derived pseudo-status (is_blocked
					// column), not a stored status, so matching the status column
					// always yields 0. Route to the is_blocked filter so
					// count --status blocked agrees with bd blocked / stats.
					b := true
					filter.Blocked = &b
				} else {
					filter.Status = &s
				}
			} else {
				for _, part := range statusParts {
					s := types.Status(strings.TrimSpace(part)).Normalize()
					if !s.IsValidWithCustom(names) {
						return HandleErrorRespectJSON("invalid status %q in multi-status filter (valid: %s)", strings.TrimSpace(part), validStatusList(names))
					}
					if s == types.StatusBlocked {
						// beads-7f3g: "blocked" is derived (is_blocked), not stored,
						// so it cannot be OR-combined with real statuses in a single
						// status-column IN() filter. Reject explicitly instead of
						// silently counting 0 for the whole multi-status filter.
						return HandleErrorRespectJSON("status %q is derived and cannot be combined in a multi-status filter; use `bd blocked` or `--status blocked` alone", "blocked")
					}
					filter.Statuses = append(filter.Statuses, s)
				}
			}
		}

		// beads-9iia: default-status-scope parity with bd list. With no explicit
		// --status (and not `--status all`), bd list excludes closed/pinned +
		// custom done/frozen statuses (list_filter.go); bd count did not, so a
		// plain `bd count` counted closed work — inflating the "how many issues"
		// answer vs the sibling `bd list` total, silently and with no flag. Mirror
		// list's default-exclude here. Skipped for --by-status (grouping is meant
		// to show every status bucket, closed included) and when the caller passed
		// an explicit status or `--status all`.
		if status == "" && groupBy != "status" {
			excludeStatuses := []types.Status{types.StatusClosed, types.StatusPinned}
			for _, cs := range filterCfg.customStatuses {
				if cs.Category == types.CategoryDone || cs.Category == types.CategoryFrozen {
					excludeStatuses = append(excludeStatuses, types.Status(cs.Name))
				}
			}
			filter.ExcludeStatus = excludeStatuses
		}

		if cmd.Flags().Changed("priority") {
			// beads-vcpq: parse via ValidatePriority (accepts 0-4 AND P0-P4),
			// mirroring bd list (list_input.go). Subsumes deud's 0-4 range check
			// — ParsePriority already rejects out-of-range and non-numeric.
			priorityStr, _ := cmd.Flags().GetString("priority")
			priority, err := validation.ValidatePriority(priorityStr)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			filter.Priority = &priority
		}
		// beads-sabd: trim read-side assignee (write side trims via llzt
		// @7f1b7dae5; read never trimmed -> padded value silently matched nothing).
		if a := strings.TrimSpace(assignee); a != "" {
			filter.Assignee = &a
		}
		if issueType != "" {
			t := issueTypeFilterValue(issueType)
			if !t.IsValidWithCustom(filterCfg.customTypes) {
				validTypes := types.ValidWorkTypesString() // beads-71j1: full 9-type list, not a stale hardcoded 6
				if len(filterCfg.customTypes) > 0 {
					validTypes += ", " + strings.Join(filterCfg.customTypes, ", ")
				}
				return HandleErrorRespectJSON("invalid issue type %q (valid: %s)", issueType, validTypes)
			}
			filter.IssueType = &t
			// beads-y06e: canonicalize issueType so the secondary consumer
			// applyCountIncludeInfra sees the normalized value. Otherwise
			// `bd count --include-infra -t GATE` sets IssueType="gate" (normalized)
			// but the raw "GATE" != "gate" appends "gate" to ExcludeTypes ->
			// filter requires gate AND excludes gate -> always 0. Residual of brxo.
			issueType = string(t)
		}
		if len(labels) > 0 {
			filter.Labels = labels
		}
		if len(labelsAny) > 0 {
			filter.LabelsAny = labelsAny
		}
		if titleSearch != "" {
			filter.TitleSearch = titleSearch
		}
		if idFilter != "" {
			ids := utils.NormalizeLabels(strings.Split(idFilter, ","))
			if len(ids) > 0 {
				filter.IDs = ids
			}
		}

		// Pattern matching
		filter.TitleContains = titleContains
		filter.DescriptionContains = descContains
		filter.NotesContains = notesContains

		// Date ranges
		if createdAfter != "" {
			t, err := parseTimeFlag(createdAfter)
			if err != nil {
				return HandleErrorRespectJSON("parsing --created-after: %v", err)
			}
			filter.CreatedAfter = &t
		}
		if createdBefore != "" {
			t, err := parseTimeFlag(createdBefore)
			if err != nil {
				return HandleErrorRespectJSON("parsing --created-before: %v", err)
			}
			filter.CreatedBefore = &t
		}
		if updatedAfter != "" {
			t, err := parseTimeFlag(updatedAfter)
			if err != nil {
				return HandleErrorRespectJSON("parsing --updated-after: %v", err)
			}
			filter.UpdatedAfter = &t
		}
		if updatedBefore != "" {
			t, err := parseTimeFlag(updatedBefore)
			if err != nil {
				return HandleErrorRespectJSON("parsing --updated-before: %v", err)
			}
			filter.UpdatedBefore = &t
		}
		if closedAfter != "" {
			t, err := parseTimeFlag(closedAfter)
			if err != nil {
				return HandleErrorRespectJSON("parsing --closed-after: %v", err)
			}
			filter.ClosedAfter = &t
		}
		if closedBefore != "" {
			t, err := parseTimeFlag(closedBefore)
			if err != nil {
				return HandleErrorRespectJSON("parsing --closed-before: %v", err)
			}
			filter.ClosedBefore = &t
		}

		// Empty/null checks
		filter.EmptyDescription = emptyDesc
		filter.NoAssignee = noAssignee
		filter.NoLabels = noLabels

		// Priority range (beads-vcpq: String + ValidatePriority, mirroring bd
		// list's --priority-min/max so P0-P4 is accepted here too).
		if cmd.Flags().Changed("priority-min") {
			s, _ := cmd.Flags().GetString("priority-min")
			p, err := validation.ValidatePriority(s)
			if err != nil {
				return HandleErrorRespectJSON("parsing --priority-min: %v", err)
			}
			filter.PriorityMin = &p
		}
		if cmd.Flags().Changed("priority-max") {
			s, _ := cmd.Flags().GetString("priority-max")
			p, err := validation.ValidatePriority(s)
			if err != nil {
				return HandleErrorRespectJSON("parsing --priority-max: %v", err)
			}
			filter.PriorityMax = &p
		}

		if includeInfra, _ := cmd.Flags().GetBool("include-infra"); includeInfra {
			cfg, err := cb.loadFilterConfig(ctx)
			if err != nil {
				// beads-huz3: honor the --json error contract on this
				// second config-load path — the sibling load at the top of
				// RunE (count.go:132) already uses HandleErrorRespectJSON, so
				// under `--json` a store/config failure here must emit a JSON
				// error object on stdout, not plain-text on stderr.
				return HandleErrorRespectJSON("%v", err)
			}
			applyCountIncludeInfra(&filter, issueType, cfg)
		} else {
			filter.SkipWisps = true // durable tier only; bd count's historical default
		}

		if groupBy == "" {
			count, err := cb.countIssues(ctx, filter)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			if jsonOutput {
				return outputJSON(struct {
					Count int64 `json:"count"`
				}{Count: count})
			}
			fmt.Println(count)
			return nil
		}

		counts, err := cb.countByGroup(ctx, filter, groupBy)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		type GroupCount struct {
			Group string `json:"group"`
			Count int    `json:"count"`
		}

		groups := make([]GroupCount, 0, len(counts))
		for group, count := range counts {
			groups = append(groups, GroupCount{Group: group, Count: count})
		}

		// --by-label buckets are not mutually exclusive, so use CountIssues for the total
		// to avoid double-counting multi-label issues.
		total, err := cb.countIssues(ctx, filter)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		slices.SortFunc(groups, func(a, b GroupCount) int {
			return cmp.Compare(a.Group, b.Group)
		})

		if jsonOutput {
			return outputJSON(struct {
				Total  int64        `json:"total"`
				Groups []GroupCount `json:"groups"`
			}{
				Total:  total,
				Groups: groups,
			})
		}
		fmt.Printf("Total: %d\n\n", total)
		for _, g := range groups {
			fmt.Printf("%s: %d\n", g.Group, g.Count)
		}
		return nil
	},
}

// countArgs rejects positional arguments. bd count is a flag-only command (it
// shares bd list's flag set), but unlike bd list it historically read no args
// and silently ignored any positional — so `bd count status=open` (a natural
// habit from `bd query status=open`) returned the grand total with exit 0
// instead of a filtered count. Mirror bd list's rejection so the mistake is
// loud, and hint key=value forms toward the corresponding --flag.
func countArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	first := args[0]
	if key, _, ok := strings.Cut(first, "="); ok && key != "" {
		if cmd.Flags().Lookup(key) != nil {
			return fmt.Errorf("bd count does not accept positional arguments; did you mean --%s? (see bd count --help)", key)
		}
	}
	return fmt.Errorf("bd count does not accept positional arguments; use flags instead (see bd count --help)")
}

// applyCountIncludeInfra switches the count filter to the wisps-inclusive
// mode of `bd list --include-infra` (GH#4387). It mirrors the buildListFilter
// defaults that determine list's cardinality so that, for any filter set,
// `bd count --include-infra <filters>` returns exactly the number of rows
// `bd list --include-infra <filters> --all` materializes:
//
//   - the wisps table is merged into the count (SkipWisps=false), picking up
//     no_history beads (durable work stored in the wisps tier) and ephemeral
//     wisps, exactly like list's merge path;
//   - template molecules are excluded (list's default without
//     --include-templates);
//   - gate beads are excluded unless gates are explicitly requested via
//     --type gate (list's default without --include-gates);
//   - counting an infra type (agent/role/message, or the store-configured
//     set) routes to the ephemeral wisps tier, like list's infra-type listing.
//
// The non-flag path never calls this function: bd count without
// --include-infra keeps its historical durable-only semantics.
func applyCountIncludeInfra(filter *types.IssueFilter, issueType string, cfg listFilterConfig) {
	filter.SkipWisps = false

	isTemplate := false
	filter.IsTemplate = &isTemplate

	if issueType != "gate" {
		filter.ExcludeTypes = append(filter.ExcludeTypes, "gate")
	}

	if issueType != "" && cfg.isInfra(issueType) {
		ephemeral := true
		filter.Ephemeral = &ephemeral
	}
}

func init() {
	// Filter flags (same as list command)
	countCmd.Flags().StringP("status", "s", "", "Filter by stored status (open, in_progress, blocked, deferred, closed). Note: dependency-blocked issues use 'bd blocked'")
	// beads-vcpq: mirror bd list's priority flag (StringP + ValidatePriority)
	// so the documented P0-P4 syntax is accepted, not just bare 0-4. An IntP
	// flag rejected "P2" with a raw cobra ParseInt error before any beads code
	// ran, breaking cross-command parity with the form bd list documents.
	registerPriorityFlag(countCmd, "")
	countCmd.Flags().StringP("assignee", "a", "", "Filter by assignee")
	countCmd.Flags().StringP("type", "t", "", "Filter by type (bug, feature, task, epic, chore, decision, merge-request, molecule, gate)")
	countCmd.Flags().StringSliceP("label", "l", []string{}, "Filter by labels (AND: must have ALL)")
	countCmd.Flags().StringSlice("label-any", []string{}, "Filter by labels (OR: must have AT LEAST ONE)")
	countCmd.Flags().String("title", "", "Filter by title text (case-insensitive substring match)")
	countCmd.Flags().String("id", "", "Filter by specific issue IDs (comma-separated)")

	// Pattern matching
	countCmd.Flags().String("title-contains", "", "Filter by title substring")
	countCmd.Flags().String("desc-contains", "", "Filter by description substring")
	countCmd.Flags().String("notes-contains", "", "Filter by notes substring")

	// Date ranges
	countCmd.Flags().String("created-after", "", "Filter issues created after date (YYYY-MM-DD or RFC3339)")
	countCmd.Flags().String("created-before", "", "Filter issues created before date (YYYY-MM-DD or RFC3339)")
	countCmd.Flags().String("updated-after", "", "Filter issues updated after date (YYYY-MM-DD or RFC3339)")
	countCmd.Flags().String("updated-before", "", "Filter issues updated before date (YYYY-MM-DD or RFC3339)")
	countCmd.Flags().String("closed-after", "", "Filter issues closed after date (YYYY-MM-DD or RFC3339)")
	countCmd.Flags().String("closed-before", "", "Filter issues closed before date (YYYY-MM-DD or RFC3339)")

	// Empty/null checks
	countCmd.Flags().Bool("empty-description", false, "Filter issues with empty description")
	countCmd.Flags().Bool("no-assignee", false, "Filter issues with no assignee")
	countCmd.Flags().Bool("no-labels", false, "Filter issues with no labels")

	// Priority ranges
	countCmd.Flags().String("priority-min", "", "Filter by minimum priority (inclusive, 0-4 or P0-P4)")
	countCmd.Flags().String("priority-max", "", "Filter by maximum priority (inclusive, 0-4 or P0-P4)")

	// Wisps tier (GH#4387): mirrors bd list's flag of the same name so
	// `bd count --include-infra <filters>` returns exactly the cardinality of
	// `bd list --include-infra <filters> --all`.
	countCmd.Flags().Bool("include-infra", false, "Include infrastructure beads and the wisps tier (matches 'bd list --include-infra --all' cardinality)")

	// Grouping flags
	countCmd.Flags().Bool("by-status", false, "Group count by status")
	countCmd.Flags().Bool("by-priority", false, "Group count by priority")
	countCmd.Flags().Bool("by-type", false, "Group count by issue type")
	countCmd.Flags().Bool("by-assignee", false, "Group count by assignee")
	countCmd.Flags().Bool("by-label", false, "Group count by label")

	rootCmd.AddCommand(countCmd)
}
