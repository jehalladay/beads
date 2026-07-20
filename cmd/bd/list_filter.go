package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
)

type listFilterConfig struct {
	customStatuses []types.CustomStatus
	customTypes    []string
	infraSet       map[string]bool
}

func (c listFilterConfig) customStatusNames() []string {
	out := make([]string, len(c.customStatuses))
	for i, s := range c.customStatuses {
		out[i] = s.Name
	}
	return out
}

func (c listFilterConfig) infraTypes() []string {
	if len(c.infraSet) == 0 {
		return domain.DefaultInfraTypes()
	}
	out := make([]string, 0, len(c.infraSet))
	for t := range c.infraSet {
		out = append(out, t)
	}
	return out
}

func (c listFilterConfig) isInfra(t string) bool {
	if len(c.infraSet) == 0 {
		return domain.IsInfraType(types.IssueType(t))
	}
	return c.infraSet[t]
}

type listFilterConfigSource interface {
	GetCustomStatuses(ctx context.Context) ([]types.CustomStatus, error)
	GetCustomTypes(ctx context.Context) ([]string, error)
	GetInfraTypes(ctx context.Context) (map[string]bool, error)
}

type directConfigSource struct{ store storage.DoltStorage }

func (d directConfigSource) GetCustomStatuses(ctx context.Context) ([]types.CustomStatus, error) {
	return d.store.GetCustomStatusesDetailed(ctx)
}
func (d directConfigSource) GetCustomTypes(ctx context.Context) ([]string, error) {
	return d.store.GetCustomTypes(ctx)
}
func (d directConfigSource) GetInfraTypes(ctx context.Context) (map[string]bool, error) {
	return d.store.GetInfraTypes(ctx), nil
}

type proxiedConfigSource struct{ uw uow.UnitOfWork }

func (p proxiedConfigSource) GetCustomStatuses(ctx context.Context) ([]types.CustomStatus, error) {
	return p.uw.ConfigUseCase().GetCustomStatuses(ctx)
}
func (p proxiedConfigSource) GetCustomTypes(ctx context.Context) ([]string, error) {
	return p.uw.ConfigUseCase().GetCustomTypes(ctx)
}
func (p proxiedConfigSource) GetInfraTypes(ctx context.Context) (map[string]bool, error) {
	return p.uw.ConfigUseCase().GetInfraTypes(ctx)
}

func loadListFilterConfig(ctx context.Context, src listFilterConfigSource) (listFilterConfig, error) {
	var cfg listFilterConfig

	statuses, err := src.GetCustomStatuses(ctx)
	if err != nil {
		return cfg, fmt.Errorf("load custom statuses: %w", err)
	}
	cfg.customStatuses = statuses

	ct, err := src.GetCustomTypes(ctx)
	if err != nil {
		return cfg, fmt.Errorf("load custom types: %w", err)
	}
	if len(ct) > 0 {
		cfg.customTypes = ct
	} else {
		cfg.customTypes = config.GetCustomTypesFromYAML()
	}

	infraSet, err := src.GetInfraTypes(ctx)
	if err != nil {
		return cfg, fmt.Errorf("load infra types: %w", err)
	}
	if len(infraSet) > 0 {
		cfg.infraSet = infraSet
	}

	return cfg, nil
}

func loadDirectListFilterConfig(ctx context.Context, store storage.DoltStorage) (listFilterConfig, error) {
	if store == nil {
		return listFilterConfig{customTypes: config.GetCustomTypesFromYAML()}, nil
	}
	return loadListFilterConfig(ctx, directConfigSource{store: store})
}

func loadProxiedListFilterConfig(ctx context.Context, uw uow.UnitOfWork) (listFilterConfig, error) {
	return loadListFilterConfig(ctx, proxiedConfigSource{uw: uw})
}

func buildListFilter(in listInput, cfg listFilterConfig) (types.IssueFilter, error) {
	filter := types.IssueFilter{
		Limit:    in.sqlLimit,
		Offset:   in.offset,
		SortBy:   in.sortBy,
		SortDesc: in.reverse,
	}

	if in.readyFlag {
		// beads-9sdix (BUG-30): --ready pins status=open. Combining it with an
		// explicit --status/--state silently DISCARDED the --status value (the
		// if-else let --ready win), so `bd list --status closed --ready` returned
		// OPEN issues with no warning. Reject the combination explicitly instead
		// of returning wrong results silently (mirrors the beads-7f3g precedent
		// that rejects a derived-status multi-combination rather than 0-ing out).
		if in.status != "" && in.status != "all" {
			return filter, fmt.Errorf("--ready cannot be combined with --status/--state (--ready already restricts to open work); drop one")
		}
		s := types.StatusOpen
		filter.Status = &s
	} else if in.status != "" && in.status != "all" {
		names := cfg.customStatusNames()
		statusParts := strings.Split(in.status, ",")
		if len(statusParts) == 1 {
			// Normalize case for built-in statuses so `--status OPEN` matches the
			// `bd query status=OPEN` behavior; custom statuses round-trip
			// unchanged (compared case-sensitively by IsValidWithCustom). beads-7wrj.
			s := types.Status(strings.TrimSpace(statusParts[0])).Normalize()
			if !s.IsValidWithCustom(names) {
				return filter, fmt.Errorf("invalid status %q (valid: %s)", in.status, validStatusList(names))
			}
			if s == types.StatusBlocked {
				// beads-7f3g: "blocked" is a derived pseudo-status (is_blocked
				// column), not a stored status value, so matching it against the
				// status column always yields 0. Route to the is_blocked filter so
				// count/list agree with bd blocked.
				b := true
				filter.Blocked = &b
			} else {
				filter.Status = &s
			}
		} else {
			for _, part := range statusParts {
				s := types.Status(strings.TrimSpace(part)).Normalize()
				if !s.IsValidWithCustom(names) {
					return filter, fmt.Errorf("invalid status %q in multi-status filter (valid: %s)", strings.TrimSpace(part), validStatusList(names))
				}
				if s == types.StatusBlocked {
					// beads-7f3g: "blocked" is derived (is_blocked), not a stored
					// status, so it cannot be OR-combined with real statuses in a
					// single status-column IN() filter. Reject explicitly instead of
					// silently returning 0 for the whole multi-status filter.
					return filter, fmt.Errorf("status %q is derived and cannot be combined in a multi-status filter; use `bd blocked` or `--status blocked` alone", "blocked")
				}
				filter.Statuses = append(filter.Statuses, s)
			}
		}
	}

	if in.status == "" && !in.allFlag && !in.readyFlag && !in.pinnedFlag {
		excludeStatuses := []types.Status{types.StatusClosed, types.StatusPinned}
		for _, cs := range cfg.customStatuses {
			if cs.Category == types.CategoryDone || cs.Category == types.CategoryFrozen {
				excludeStatuses = append(excludeStatuses, types.Status(cs.Name))
			}
		}
		filter.ExcludeStatus = excludeStatuses
	}

	if in.prioritySet {
		p := in.priority
		filter.Priority = &p
	}
	// beads-sabd: trim the read-side assignee filter (write side trims via
	// llzt @7f1b7dae5; read matches case-insensitively but never trimmed, so
	// a padded value silently matched nothing). Guard on the TRIMMED value so a
	// whitespace-only flag doesn't collapse to an empty-assignee filter.
	if a := strings.TrimSpace(in.assignee); a != "" {
		filter.Assignee = &a
	}
	if in.issueType != "" {
		t := types.IssueType(in.issueType)
		if !t.IsValidWithCustom(cfg.customTypes) {
			validTypes := "bug, feature, task, epic, chore, decision"
			if len(cfg.customTypes) > 0 {
				validTypes += ", " + joinStrings(cfg.customTypes, ", ")
			}
			return filter, fmt.Errorf("invalid issue type %q (valid: %s)", in.issueType, validTypes)
		}
		filter.IssueType = &t
	}

	if len(in.labels) > 0 {
		filter.Labels = in.labels
	}
	if len(in.labelsAny) > 0 {
		filter.LabelsAny = in.labelsAny
	}
	if len(in.excludeLabels) > 0 {
		filter.ExcludeLabels = in.excludeLabels
	}
	if in.labelPattern != "" {
		filter.LabelPattern = in.labelPattern
	}
	if in.labelRegex != "" {
		filter.LabelRegex = in.labelRegex
	}
	if in.titleSearch != "" {
		filter.TitleSearch = in.titleSearch
	}
	if in.idFilter != "" {
		ids := utils.NormalizeLabels(strings.Split(in.idFilter, ","))
		if len(ids) > 0 {
			filter.IDs = ids
		}
	}
	if in.specPrefix != "" {
		filter.SpecIDPrefix = in.specPrefix
	}

	if in.titleContains != "" {
		filter.TitleContains = in.titleContains
	}
	if in.descContains != "" {
		filter.DescriptionContains = in.descContains
	}
	if in.notesContains != "" {
		filter.NotesContains = in.notesContains
	}

	filter.CreatedAfter = in.createdAfter
	filter.CreatedBefore = in.createdBefore
	filter.UpdatedAfter = in.updatedAfter
	filter.UpdatedBefore = in.updatedBefore
	filter.ClosedAfter = in.closedAfter
	filter.ClosedBefore = in.closedBefore

	if in.emptyDesc {
		filter.EmptyDescription = true
	}
	if in.noAssignee {
		filter.NoAssignee = true
	}
	if in.noLabels {
		filter.NoLabels = true
	}
	if in.skipLabels {
		filter.SkipLabels = true
	}

	if in.priorityMinSet {
		p := in.priorityMin
		filter.PriorityMin = &p
	}
	if in.priorityMaxSet {
		p := in.priorityMax
		filter.PriorityMax = &p
	}

	if in.pinnedFlag {
		pinned := true
		filter.Pinned = &pinned
	} else if in.noPinnedFlag || (in.status != "pinned" && in.status != "hooked" && !in.allFlag) {
		pinned := false
		filter.Pinned = &pinned
	}

	if !in.includeTemplates {
		isTemplate := false
		filter.IsTemplate = &isTemplate
	}

	if !in.includeGates && in.issueType != "gate" {
		filter.ExcludeTypes = append(filter.ExcludeTypes, "gate")
	}

	if !in.includeInfra && !cfg.isInfra(in.issueType) {
		for _, t := range cfg.infraTypes() {
			filter.ExcludeTypes = append(filter.ExcludeTypes, types.IssueType(t))
		}
	}

	for _, raw := range in.excludeTypeStrs {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				filter.ExcludeTypes = append(filter.ExcludeTypes, types.IssueType(utils.NormalizeIssueType(t)))
			}
		}
	}

	if cfg.isInfra(in.issueType) {
		ephemeral := true
		filter.Ephemeral = &ephemeral
	}

	if in.parentID != "" {
		pid := in.parentID
		filter.ParentID = &pid
	}
	if in.noParent {
		filter.NoParent = true
	}

	if in.molType != nil {
		filter.MolType = in.molType
	}
	if in.wispType != nil {
		filter.WispType = in.wispType
	}

	if in.deferredFlag {
		filter.Deferred = true
	}
	filter.DeferAfter = in.deferAfter
	filter.DeferBefore = in.deferBefore
	filter.DueAfter = in.dueAfter
	filter.DueBefore = in.dueBefore
	if in.overdueFlag {
		filter.Overdue = true
	}

	if len(in.metadataFields) > 0 {
		filter.MetadataFields = in.metadataFields
	}
	if in.hasMetadataKey != "" {
		filter.HasMetadataKey = in.hasMetadataKey
	}

	if !in.includeInfra && (in.issueType == "" || !cfg.isInfra(in.issueType)) {
		filter.SkipWisps = true
	}

	return filter, nil
}

func validStatusList(customStatusNames []string) string {
	validList := "open, in_progress, blocked, deferred, closed, pinned, hooked"
	if len(customStatusNames) > 0 {
		validList += ", " + strings.Join(customStatusNames, ", ")
	}
	return validList
}
