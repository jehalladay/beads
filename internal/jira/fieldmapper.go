package jira

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// beadsDeferredLabel is a marker label that preserves the beads "deferred"
// status across a Jira round-trip (beads-n1y7). Jira's default workflow has no
// native "deferred" state (To Do / In Progress / Done), so StatusToTracker maps
// deferred → "To Do" and StatusToBeads would reimport it as "open" — silently
// downgrading the work-planning semantics. Emitting this label on push (when no
// custom statusMap entry claims deferred) and recovering it on import makes the
// round-trip lossless, matching how ado (Removed state) and gitlab
// (status::deferred label) already preserve it. Jira supports issue labels, so
// this rides the existing label push/import path.
const beadsDeferredLabel = "bd:status:deferred"

// jiraFieldMapper implements tracker.FieldMapper for Jira.
type jiraFieldMapper struct {
	apiVersion       string                            // "2" or "3" (default: "3")
	statusMap        map[string]string                 // beads status → Jira status name (from jira.status_map.* config)
	typeMap          map[string]string                 // beads type → Jira type (from jira.type_map.* config)
	priorityMap      map[string]string                 // beads priority (as string "0"-"4") → Jira priority name (from jira.priority_map.* config)
	customFields     map[string]interface{}            // Jira field name/id → value (from jira.custom_fields.* config)
	typeCustomFields map[string]map[string]interface{} // Jira issue type → field name/id → value
}

func (m *jiraFieldMapper) PriorityToBeads(trackerPriority interface{}) int {
	if name, ok := trackerPriority.(string); ok {
		// Check custom map first (inverted: Jira name → beads priority).
		for beadsPri, jiraName := range m.priorityMap {
			if strings.EqualFold(name, jiraName) {
				if v, err := strconv.Atoi(beadsPri); err == nil && v >= 0 && v <= 4 {
					return v
				}
			}
		}
		// Jira defaults.
		switch name {
		case "Highest":
			return 0
		case "High":
			return 1
		case "Medium":
			return 2
		case "Low":
			return 3
		case "Lowest":
			return 4
		}
	}
	return 2
}

func (m *jiraFieldMapper) PriorityToTracker(beadsPriority int) interface{} {
	// Check custom map first (beads priority as string key → Jira name).
	if m.priorityMap != nil {
		key := strconv.Itoa(beadsPriority)
		if name, ok := m.priorityMap[key]; ok {
			return name
		}
	}
	// Jira defaults.
	switch beadsPriority {
	case 0:
		return "Highest"
	case 1:
		return "High"
	case 2:
		return "Medium"
	case 3:
		return "Low"
	case 4:
		return "Lowest"
	default:
		return "Medium"
	}
}

func (m *jiraFieldMapper) StatusToBeads(trackerState interface{}) types.Status {
	if state, ok := trackerState.(string); ok {
		// Check custom map first (inverted: jira name → beads status).
		for beadsStatus, jiraName := range m.statusMap {
			if strings.EqualFold(state, jiraName) {
				return types.Status(beadsStatus)
			}
		}
		switch state {
		case "To Do", "Open", "Backlog", "New":
			return types.StatusOpen
		case "In Progress", "In Review":
			return types.StatusInProgress
		case "Blocked":
			return types.StatusBlocked
		case "Done", "Closed", "Resolved":
			return types.StatusClosed
		}
	}
	return types.StatusOpen
}

func (m *jiraFieldMapper) StatusToTracker(beadsStatus types.Status) interface{} {
	// Check custom map first.
	if name, ok := m.statusMap[string(beadsStatus)]; ok {
		return name
	}
	switch beadsStatus {
	case types.StatusOpen:
		return "To Do"
	case types.StatusInProgress:
		return "In Progress"
	case types.StatusBlocked:
		return "Blocked"
	case types.StatusClosed:
		return "Done"
	default:
		return "To Do"
	}
}

func (m *jiraFieldMapper) TypeToBeads(trackerType interface{}) types.IssueType {
	t, ok := trackerType.(string)
	if !ok {
		return types.TypeTask
	}

	// Check custom map first (inverted: Jira type → beads type).
	for beadsType, jiraType := range m.typeMap {
		if strings.EqualFold(t, jiraType) {
			return types.IssueType(beadsType)
		}
	}

	// Jira defaults.
	switch t {
	case "Bug":
		return types.TypeBug
	case "Story", "Feature":
		return types.TypeFeature
	case "Epic":
		return types.TypeEpic
	case "Task", "Sub-task":
		return types.TypeTask
	}
	return types.TypeTask
}

func (m *jiraFieldMapper) TypeToTracker(beadsType types.IssueType) interface{} {
	if name, ok := m.typeMap[string(beadsType)]; ok {
		return name
	}
	switch beadsType {
	case types.TypeBug:
		return "Bug"
	case types.TypeFeature:
		return "Story"
	case types.TypeEpic:
		return "Epic"
	default:
		return "Task"
	}
}

func (m *jiraFieldMapper) IssueToBeads(ti *tracker.TrackerIssue) *tracker.IssueConversion {
	ji, ok := ti.Raw.(*Issue)
	if !ok || ji == nil {
		return nil
	}

	issue := &types.Issue{
		Title:       ji.Fields.Summary,
		Description: DescriptionToPlainText(ji.Fields.Description),
		Priority:    m.PriorityToBeads(priorityName(ji)),
		Status:      m.StatusToBeads(statusName(ji)),
		IssueType:   m.TypeToBeads(typeName(ji)),
	}

	if ji.Fields.Assignee != nil {
		issue.Owner = ji.Fields.Assignee.DisplayName
	}

	if ji.Fields.Labels != nil {
		// Recover a beads "deferred" status preserved via the marker label
		// (beads-n1y7). A custom statusMap entry, if present, already yields
		// the correct status via StatusToBeads, so only override when the
		// marker is present. Strip the marker so it does not leak into the
		// user-facing label set.
		labels := ji.Fields.Labels
		if hasLabel(labels, beadsDeferredLabel) {
			issue.Status = types.StatusDeferred
			labels = stripLabel(labels, beadsDeferredLabel)
		}
		issue.Labels = labels
	}

	// Set external ref from issue URL
	if ji.Self != "" {
		ref := extractBrowseURL(ji)
		issue.ExternalRef = &ref
	}

	return &tracker.IssueConversion{
		Issue: issue,
	}
}

// maxSummaryLength is Jira's hard cap on the summary field. Jira 400-rejects a
// create/update whose summary exceeds this, failing the whole push.
const maxSummaryLength = 255

// truncateSummary caps a title at Jira's summary limit (maxSummaryLength),
// appending an ellipsis marker so the truncation is visible on the Jira side
// (beads-a2lv). A local beads title may be up to 500 chars, but Jira 400-rejects
// anything over maxSummaryLength — so the PUSHED copy is truncated while the
// local title is left untouched. Rune-aware so a multi-byte character is never
// split. Mirrors the landed ado truncateTitle pattern (beads-z5ys).
func truncateSummary(title string) string {
	if len(title) <= maxSummaryLength {
		return title
	}
	const marker = "..."
	runes := []rune(title)
	// If the rune count already fits, the byte-length overflow was from
	// multi-byte runes — Jira's limit is on characters, so it is fine as-is.
	if len(runes) <= maxSummaryLength {
		return title
	}
	keep := maxSummaryLength - len([]rune(marker))
	if keep < 0 {
		keep = 0
	}
	return string(runes[:keep]) + marker
}

// validateJiraLabels fails loud, before any Jira API call, if a label contains
// internal whitespace. Jira labels are single-token: the server 400-rejects a
// label with a space/tab/newline, failing the WHOLE issue push with an opaque
// error. beads labels are only edge-trimmed (utils.NormalizeLabels) so a label
// like "needs review" is legal locally but unmappable to Jira. There is no
// lossless transform (space→underscore would drift on pull-back and break
// dedup), so per beads-xcbd the correct behavior is to reject with a beads-side
// message naming the offending label, rather than silently drop or transform
// it. Jira-specific: GitLab labels allow spaces and ADO Tags are "; "-joined.
func validateJiraLabels(labels []string) error {
	for _, l := range labels {
		if strings.IndexFunc(l, unicode.IsSpace) >= 0 {
			return fmt.Errorf("label %q contains whitespace; Jira rejects it — rename or remove internal spaces before syncing", l)
		}
	}
	return nil
}

func (m *jiraFieldMapper) IssueToTracker(issue *types.Issue) map[string]interface{} {
	fields := map[string]interface{}{
		"summary": truncateSummary(issue.Title),
	}

	// v3 requires ADF (Atlassian Document Format); v2 accepts a plain string.
	if issue.Description != "" {
		if m.apiVersion == "2" {
			fields["description"] = issue.Description
		} else {
			fields["description"] = PlainTextToADF(issue.Description)
		}
	}

	// Set issue type
	typeName := m.TypeToTracker(issue.IssueType)
	if name, ok := typeName.(string); ok {
		fields["issuetype"] = map[string]string{"name": name}
	}

	// Set priority
	priorityName := m.PriorityToTracker(issue.Priority)
	if name, ok := priorityName.(string); ok {
		fields["priority"] = map[string]string{"name": name}
	}

	// Set labels. Preserve a beads "deferred" status via a marker label when
	// Jira cannot represent it natively (beads-n1y7): if the operator has
	// configured a custom statusMap entry for deferred, StatusToTracker already
	// pushes the real status and the round-trip is lossless, so the marker is
	// only needed for the default To-Do fallback.
	labels := issue.Labels
	if issue.Status == types.StatusDeferred {
		if _, mapped := m.statusMap[string(types.StatusDeferred)]; !mapped && !hasLabel(labels, beadsDeferredLabel) {
			labels = append(labels, beadsDeferredLabel)
		}
	}
	if len(labels) > 0 {
		fields["labels"] = labels
	}

	for fieldName, value := range m.customFields {
		fields[fieldName] = value
	}

	if name, ok := typeName.(string); ok {
		for jiraType, customFields := range m.typeCustomFields {
			if !strings.EqualFold(jiraType, name) {
				continue
			}
			for fieldName, value := range customFields {
				fields[fieldName] = value
			}
		}
	}

	return fields
}

// hasLabel reports whether labels contains target (case-sensitive).
func hasLabel(labels []string, target string) bool {
	for _, l := range labels {
		if l == target {
			return true
		}
	}
	return false
}

// stripLabel returns labels with every occurrence of target removed, preserving
// order. It allocates a fresh slice so the caller's input is left untouched.
func stripLabel(labels []string, target string) []string {
	if len(labels) == 0 {
		return labels
	}
	filtered := make([]string, 0, len(labels))
	for _, l := range labels {
		if l == target {
			continue
		}
		filtered = append(filtered, l)
	}
	return filtered
}

// Helper functions for safe field extraction from Jira issues.

func priorityName(ji *Issue) string {
	if ji.Fields.Priority != nil {
		return ji.Fields.Priority.Name
	}
	return ""
}

func statusName(ji *Issue) string {
	if ji.Fields.Status != nil {
		return ji.Fields.Status.Name
	}
	return ""
}

func typeName(ji *Issue) string {
	if ji.Fields.IssueType != nil {
		return ji.Fields.IssueType.Name
	}
	return ""
}

// extractBrowseURL builds the human-readable browse URL from a Jira issue.
// Self is "https://company.atlassian.net/rest/api/3/issue/10001";
// we need "https://company.atlassian.net/browse/PROJ-123".
func extractBrowseURL(ji *Issue) string {
	if ji.Self == "" || ji.Key == "" {
		return ""
	}
	if idx := strings.Index(ji.Self, "/rest/api/"); idx > 0 {
		return ji.Self[:idx] + "/browse/" + ji.Key
	}
	return ""
}
