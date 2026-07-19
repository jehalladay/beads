package main

import (
	"fmt"
	"os"
	"strings"

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
