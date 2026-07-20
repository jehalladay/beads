package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/steveyegge/beads/internal/validation"
)

type readyInput struct {
	filter       types.WorkFilter
	limit        int
	offset       int
	claim        bool
	gated        bool
	molID        string
	explain      bool
	prettyFormat bool
	plainFormat  bool
	parentID     string
	jsonOut      bool
}

// dateRangeAxis names an after/before date-range pair for reversed-range
// validation (beads-tjysi).
type dateRangeAxis struct {
	name   string
	after  *time.Time
	before *time.Time
}

// reversedDateRangeMessage returns an error message for the first axis whose
// after > before (an always-false WHERE that would silently return empty), or
// "" if all axes are ordered or unset. Equal bounds (after==before) are valid.
// Callers wrap the message in their path-appropriate error sink (the direct
// path returns HandleErrorRespectJSON; the proxied path calls
// FatalErrorRespectJSON) — beads-tjysi, parity with bd list wnm6g/BUG-37.
func reversedDateRangeMessage(axes ...dateRangeAxis) string {
	for _, a := range axes {
		if a.after != nil && a.before != nil && a.after.After(*a.before) {
			return fmt.Sprintf("--%s-after (%s) cannot be later than --%s-before (%s)",
				a.name, a.after.Format("2006-01-02"), a.name, a.before.Format("2006-01-02"))
		}
	}
	return ""
}

func gatherReadyInput(cmd *cobra.Command) readyInput {
	in := readyInput{}

	in.claim, _ = cmd.Flags().GetBool("claim")
	in.gated, _ = cmd.Flags().GetBool("gated")
	in.molID, _ = cmd.Flags().GetString("mol")
	in.explain, _ = cmd.Flags().GetBool("explain")
	in.prettyFormat, _ = cmd.Flags().GetBool("pretty")
	in.plainFormat, _ = cmd.Flags().GetBool("plain")
	in.jsonOut = jsonOutput

	in.limit, _ = cmd.Flags().GetInt("limit")
	// Reject a negative --limit here (beads-eqi4): gatherReadyInput is the shared
	// input path for BOTH direct ready and runReadyProxiedServer, so guarding it
	// once covers both. Without it a negative --limit silently unbounds (the SQL
	// builders apply filter.Limit only when >0). Mirrors the --offset guard below
	// and bd list (uh4i). FatalError matches the offset guard's exit style.
	if cmd.Flags().Changed("limit") && in.limit < 0 {
		FatalError("--limit must be >= 0")
	}
	if cmd.Flags().Changed("offset") {
		offset, _ := cmd.Flags().GetInt("offset")
		if offset < 0 {
			FatalError("--offset must be >= 0")
		}
		in.offset = offset
	}
	assignee, _ := cmd.Flags().GetString("assignee")
	// beads-sabd: trim read-side assignee (write side trims via llzt @7f1b7dae5;
	// read never trimmed -> padded value silently matched nothing). Mirrors ready.go.
	assignee = strings.TrimSpace(assignee)
	unassigned, _ := cmd.Flags().GetBool("unassigned")
	sortPolicy, _ := cmd.Flags().GetString("sort")
	labels, _ := cmd.Flags().GetStringSlice("label")
	labelsAny, _ := cmd.Flags().GetStringSlice("label-any")
	excludeLabels, _ := cmd.Flags().GetStringSlice("exclude-label")
	issueType, _ := cmd.Flags().GetString("type")
	issueType = utils.NormalizeIssueType(issueType)
	in.parentID, _ = cmd.Flags().GetString("parent")
	molTypeStr, _ := cmd.Flags().GetString("mol-type")
	includeDeferred, _ := cmd.Flags().GetBool("include-deferred")
	includeEphemeral, _ := cmd.Flags().GetBool("include-ephemeral")
	excludeTypeStrs, _ := cmd.Flags().GetStringSlice("exclude-type")

	// beads-gddf: validate --type here so the PROXIED ready path rejects an
	// invalid type like list/count/search do. gatherReadyInput is the shared
	// input path for runReadyProxiedServer (the direct ready.go RunE guards
	// --type itself, since it does NOT go through gatherReadyInput — same split
	// as the --limit guard above). Without this, `bd ready --type bogus` dropped
	// the raw string into WorkFilter.Type and matched nothing (rc0, empty) — a
	// false-negative footgun. Custom-type-aware (loadEmbeddedCustomTypes) to
	// match count/list and not reject a user's configured custom type.
	if issueType != "" {
		if t := types.IssueType(issueType); !t.IsValidWithCustom(loadEmbeddedCustomTypes()) {
			validTypes := types.ValidWorkTypesString()
			if custom := loadEmbeddedCustomTypes(); len(custom) > 0 {
				validTypes += ", " + strings.Join(custom, ", ")
			}
			FatalErrorRespectJSON("invalid issue type %q (valid: %s)", issueType, validTypes)
		}
	}

	var molType *types.MolType
	if molTypeStr != "" {
		mt := types.MolType(molTypeStr)
		if !mt.IsValid() {
			FatalError("invalid mol-type %q (must be swarm, patrol, or work)", molTypeStr)
		}
		molType = &mt
	}

	if in.claim && assignee != "" {
		FatalErrorRespectJSON("--claim cannot be combined with --assignee")
	}
	if in.claim && in.gated {
		FatalErrorRespectJSON("--claim cannot be combined with --gated")
	}
	if in.claim && in.molID != "" {
		FatalErrorRespectJSON("--claim cannot be combined with --mol")
	}
	if in.claim && in.explain {
		FatalErrorRespectJSON("--claim cannot be combined with --explain")
	}
	if in.offset > 0 && in.claim {
		FatalErrorRespectJSON("--offset cannot be combined with --claim")
	}
	if in.offset > 0 && in.gated {
		FatalErrorRespectJSON("--offset cannot be combined with --gated")
	}
	if in.offset > 0 && in.molID != "" {
		FatalErrorRespectJSON("--offset cannot be combined with --mol")
	}
	if in.offset > 0 && in.explain {
		FatalErrorRespectJSON("--offset cannot be combined with --explain")
	}

	labels = utils.NormalizeLabels(labels)
	labelsAny = utils.NormalizeLabels(labelsAny)
	excludeLabels = utils.NormalizeLabels(excludeLabels)

	if len(labels) == 0 && len(labelsAny) == 0 {
		if dirLabels := config.GetDirectoryLabels(); len(dirLabels) > 0 {
			labelsAny = dirLabels
		}
	}

	var excludeTypes []types.IssueType
	for _, raw := range excludeTypeStrs {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				excludeTypes = append(excludeTypes, types.IssueType(utils.NormalizeIssueType(t)))
			}
		}
	}

	in.filter = types.WorkFilter{
		Status:           "open",
		Type:             issueType,
		Limit:            in.limit,
		Offset:           in.offset,
		Unassigned:       unassigned,
		SortPolicy:       types.SortPolicy(sortPolicy),
		Labels:           labels,
		LabelsAny:        labelsAny,
		ExcludeLabels:    excludeLabels,
		IncludeDeferred:  includeDeferred,
		IncludeEphemeral: includeEphemeral,
		ExcludeTypes:     excludeTypes,
	}
	if cmd.Flags().Changed("priority") {
		// beads-57tt: parse via ValidatePriority (StringP flag) so the PROXIED
		// ready path rejects an out-of-range/non-numeric --priority like
		// list/count/create — gatherReadyInput is the shared input path for
		// runReadyProxiedServer (the direct ready.go RunE guards it separately,
		// since it does NOT go through gatherReadyInput — same split as --limit
		// and beads-gddf --type). FatalErrorRespectJSON matches the neighbors.
		priorityStr, _ := cmd.Flags().GetString("priority")
		priority, err := validation.ValidatePriority(priorityStr)
		if err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		in.filter.Priority = &priority
	}
	// beads-cseh3: --priority-min/--priority-max range on the PROXIED ready path
	// too (parity with bd list/count). Shared input path, so guarding here covers
	// runReadyProxiedServer; the direct ready.go RunE guards separately (same
	// split as --priority / --type). FatalErrorRespectJSON matches the neighbors.
	if cmd.Flags().Changed("priority-min") {
		s, _ := cmd.Flags().GetString("priority-min")
		p, err := validation.ValidatePriority(s)
		if err != nil {
			FatalErrorRespectJSON("parsing --priority-min: %v", err)
		}
		in.filter.PriorityMin = &p
	}
	if cmd.Flags().Changed("priority-max") {
		s, _ := cmd.Flags().GetString("priority-max")
		p, err := validation.ValidatePriority(s)
		if err != nil {
			FatalErrorRespectJSON("parsing --priority-max: %v", err)
		}
		in.filter.PriorityMax = &p
	}
	// beads-tjysi: reject a reversed priority range on the PROXIED ready path too
	// (parity with the direct ready.go guard + bd list wnm6g). FatalErrorRespectJSON
	// matches the neighbors here (gatherReadyInput returns readyInput, not error).
	if in.filter.PriorityMin != nil && in.filter.PriorityMax != nil && *in.filter.PriorityMin > *in.filter.PriorityMax {
		FatalErrorRespectJSON("--priority-min (%d) cannot be greater than --priority-max (%d)", *in.filter.PriorityMin, *in.filter.PriorityMax)
	}
	// beads-6na9a: --desc-contains on the PROXIED ready path too (parity with
	// bd list). Shared input path, so setting it here covers runReadyProxiedServer;
	// the direct ready.go RunE sets it separately (same split as --priority).
	if dc, _ := cmd.Flags().GetString("desc-contains"); dc != "" {
		in.filter.DescriptionContains = dc
	}
	// beads-j95lq: --notes-contains on the PROXIED ready path too (parity with
	// bd list). Shared input path, so setting it here covers runReadyProxiedServer;
	// the direct ready.go RunE sets it separately (same split as --desc-contains).
	if nc, _ := cmd.Flags().GetString("notes-contains"); nc != "" {
		in.filter.NotesContains = nc
	}
	// beads-d1as8: --title-contains on the PROXIED ready path too (parity with
	// bd list). Shared input path, so setting it here covers runReadyProxiedServer;
	// the direct ready.go RunE sets it separately (same split as --priority).
	if tc, _ := cmd.Flags().GetString("title-contains"); tc != "" {
		in.filter.TitleContains = tc
	}
	// beads-gqcmu: --no-labels / --empty-description on the PROXIED ready path too
	// (parity with bd list). Plain bools; the direct ready.go RunE sets them separately.
	in.filter.NoLabels, _ = cmd.Flags().GetBool("no-labels")
	in.filter.EmptyDescription, _ = cmd.Flags().GetBool("empty-description")
	// beads-10y4y: created/updated date-range filters on the PROXIED ready path
	// too (parity with bd list). Shared input path, so parsing here covers
	// runReadyProxiedServer; the direct ready.go RunE parses separately (same
	// split as --priority). parseTimeFlag is the same relative-time parser
	// parseListTimeFlag wraps; FatalErrorRespectJSON matches the neighbors.
	for _, tf := range []struct {
		name string
		dst  **time.Time
	}{
		{"created-after", &in.filter.CreatedAfter},
		{"created-before", &in.filter.CreatedBefore},
		{"updated-after", &in.filter.UpdatedAfter},
		{"updated-before", &in.filter.UpdatedBefore},
		// beads-zmtp6: due_at range on the PROXIED ready path too (parity with bd list).
		{"due-after", &in.filter.DueAfter},
		{"due-before", &in.filter.DueBefore},
	} {
		if s, _ := cmd.Flags().GetString(tf.name); s != "" {
			// beads-ci44e: an upper-bound flag (--X-before) snaps a bare date to
			// END-of-day (parity with the direct ready + bd list paths); lower
			// bounds keep start-of-day.
			var (
				t   time.Time
				err error
			)
			if strings.HasSuffix(tf.name, "-before") {
				t, err = parseUpperBoundTimeFlag(s)
			} else {
				t, err = parseTimeFlag(s)
			}
			if err != nil {
				FatalErrorRespectJSON("parsing --%s: %v", tf.name, err)
			}
			*tf.dst = &t
		}
	}
	// beads-tjysi: reject reversed date ranges on the PROXIED ready path too
	// (parity with the direct ready.go guard + bd list wnm6g/BUG-37).
	if msg := reversedDateRangeMessage(
		dateRangeAxis{"created", in.filter.CreatedAfter, in.filter.CreatedBefore},
		dateRangeAxis{"updated", in.filter.UpdatedAfter, in.filter.UpdatedBefore},
		dateRangeAxis{"due", in.filter.DueAfter, in.filter.DueBefore},
	); msg != "" {
		FatalErrorRespectJSON("%s", msg)
	}
	// beads-zmtp6: --overdue on the PROXIED ready path too (parity with bd list).
	in.filter.Overdue, _ = cmd.Flags().GetBool("overdue")
	if assignee != "" && !unassigned {
		in.filter.Assignee = &assignee
	}
	if in.parentID != "" {
		in.filter.ParentID = &in.parentID
	}
	if molType != nil {
		in.filter.MolType = molType
	}

	metadataFieldFlags, _ := cmd.Flags().GetStringArray("metadata-field")
	if len(metadataFieldFlags) > 0 {
		in.filter.MetadataFields = make(map[string]string, len(metadataFieldFlags))
		for _, mf := range metadataFieldFlags {
			k, v, ok := strings.Cut(mf, "=")
			if !ok || k == "" {
				fmt.Fprintf(os.Stderr, "Error: invalid --metadata-field: expected key=value, got %q\n", mf)
				os.Exit(1)
			}
			if err := storage.ValidateMetadataKey(k); err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid --metadata-field key: %v\n", err)
				os.Exit(1)
			}
			in.filter.MetadataFields[k] = v
		}
	}
	hasMetadataKey, _ := cmd.Flags().GetString("has-metadata-key")
	if hasMetadataKey != "" {
		if err := storage.ValidateMetadataKey(hasMetadataKey); err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid --has-metadata-key: %v\n", err)
			os.Exit(1)
		}
		in.filter.HasMetadataKey = hasMetadataKey
	}

	if !in.filter.SortPolicy.IsValid() {
		FatalError("invalid sort policy '%s'. Valid values: hybrid, priority, oldest", sortPolicy)
	}

	return in
}
