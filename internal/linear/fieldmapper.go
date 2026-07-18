package linear

import (
	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// maxTitleLength is Linear's server-side cap on an issue title. Linear rejects
// a create/update whose title exceeds 255 characters, while a local beads title
// may be up to 500 (see types Validate). beads-exaq.
const maxTitleLength = 255

// truncateTitle caps a title at Linear's maxTitleLength, appending an ellipsis
// marker so the truncation is visible on the Linear side (beads-exaq). The
// PUSHED copy is truncated while the local beads title is left untouched.
// Rune-aware so a multi-byte character is never split. Mirrors the GitHub
// (beads-yaum), GitLab (beads-h266), Jira (beads-a2lv), and ADO (beads-z5ys)
// guards — the reusable SCM per-field-cap class.
func truncateTitle(title string) string {
	// Linear counts characters (runes), not bytes; a title whose rune count
	// already fits is fine even if its byte length overflows from multi-byte
	// runes.
	if len([]rune(title)) <= maxTitleLength {
		return title
	}
	const marker = "..."
	runes := []rune(title)
	keep := maxTitleLength - len([]rune(marker))
	if keep < 0 {
		keep = 0
	}
	return string(runes[:keep]) + marker
}

// linearFieldMapper implements tracker.FieldMapper for Linear.
type linearFieldMapper struct {
	config *MappingConfig
}

func (m *linearFieldMapper) PriorityToBeads(trackerPriority interface{}) int {
	if p, ok := trackerPriority.(int); ok {
		return PriorityToBeads(p, m.config)
	}
	return 2
}

func (m *linearFieldMapper) PriorityToTracker(beadsPriority int) interface{} {
	return PriorityToLinear(beadsPriority, m.config)
}

func (m *linearFieldMapper) StatusToBeads(trackerState interface{}) types.Status {
	if state, ok := trackerState.(*State); ok {
		return StateToBeadsStatus(state, m.config)
	}
	return types.StatusOpen
}

func (m *linearFieldMapper) StatusToTracker(beadsStatus types.Status) interface{} {
	return StatusToLinearStateType(beadsStatus)
}

func (m *linearFieldMapper) TypeToBeads(trackerType interface{}) types.IssueType {
	if labels, ok := trackerType.(*Labels); ok {
		return LabelToIssueType(labels, m.config)
	}
	return types.TypeTask
}

func (m *linearFieldMapper) TypeToTracker(beadsType types.IssueType) interface{} {
	return string(beadsType)
}

func (m *linearFieldMapper) IssueToBeads(ti *tracker.TrackerIssue) *tracker.IssueConversion {
	li, ok := ti.Raw.(*Issue)
	if !ok {
		return nil
	}

	conv := IssueToBeads(li, m.config)
	if conv == nil {
		return nil
	}

	issue, ok := conv.Issue.(*types.Issue)
	if !ok {
		return nil
	}

	var deps []tracker.DependencyInfo
	for _, d := range conv.Dependencies {
		deps = append(deps, tracker.DependencyInfo{
			FromExternalID: d.FromLinearID,
			ToExternalID:   d.ToLinearID,
			Type:           d.Type,
			Source:         tracker.DependencySource(d.Source),
		})
	}

	return &tracker.IssueConversion{
		Issue:        issue,
		Dependencies: deps,
	}
}

func (m *linearFieldMapper) IssueToTracker(issue *types.Issue) map[string]interface{} {
	updates := map[string]interface{}{
		"title":    truncateTitle(issue.Title),
		"priority": PriorityToLinear(issue.Priority, m.config),
	}
	// Omit an empty description so a local issue with no body does not
	// overwrite (wipe) a non-empty description on the external Linear issue
	// during an update. Matches jira's intended guard; see beads-fmb9.
	if issue.Description != "" {
		updates["description"] = issue.Description
	}
	return updates
}
