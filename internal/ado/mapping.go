package ado

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// IssueToBeads converts an ADO WorkItem (via TrackerIssue) to a beads Issue.
// Returns nil if the TrackerIssue's Raw field is not a *WorkItem.
func (m *adoFieldMapper) IssueToBeads(ti *tracker.TrackerIssue) *tracker.IssueConversion {
	if ti == nil {
		return nil
	}
	wi, ok := ti.Raw.(*WorkItem)
	if !ok || wi == nil {
		return nil
	}

	// Convert description from HTML to Markdown.
	desc, _ := HTMLToMarkdown(wi.GetStringField(FieldDescription))

	// Extract owner from AssignedTo (can be string or identity map).
	owner := extractAssignedTo(wi.GetField(FieldAssignedTo))

	// Parse tags, filtering out internal beads:* tags.
	allTags := parseTags(wi.GetStringField(FieldTags))
	labels := filterBeadsTags(allTags)

	issue := &types.Issue{
		Title:       wi.GetStringField(FieldTitle),
		Description: desc,
		Priority:    m.PriorityToBeads(wi.GetField(FieldPriority)),
		Status:      m.StatusToBeads(wi.GetField(FieldState)),
		IssueType:   m.TypeToBeads(wi.GetField(FieldWorkItemType)),
		Owner:       owner,
		Labels:      labels,
	}

	// Restore blocked status from beads:blocked tag (ADO has no blocked state,
	// so blocked maps to Active + tag on push; reverse it here on pull).
	if issue.Status == types.StatusInProgress && hasBeadsTag(wi.GetStringField(FieldTags), "beads:blocked") {
		issue.Status = types.StatusBlocked
	}

	// Restore the exact beads priority when the ADO mapping is lossy (beads 3 and
	// 4 both map to ADO priority 4). The value round-trips as a beads:priority:N
	// tag (see IssueToTracker) — the beads_priority metadata channel used to hold
	// it but was never sent to ADO, so the tag is the only reliable source on pull.
	if p, ok := priorityFromTags(allTags); ok {
		issue.Priority = p
	} else if ti.Metadata != nil {
		// Backwards-compat: honor a beads_priority carried in tracker metadata if
		// some caller still supplies it (e.g. a synthesized TrackerIssue).
		if bp, ok := ti.Metadata["beads_priority"]; ok {
			if p, valid := parseBeadsPriorityValue(bp); valid {
				issue.Priority = p
			}
		}
	}

	// Build external ref URL.
	ref := buildExternalRef(wi)
	if ref != "" {
		issue.ExternalRef = &ref
	}

	// Preserve ADO-specific metadata for round-trip fidelity.
	meta := buildMetadata(wi)
	// Carry forward beads_priority from TrackerIssue metadata so it survives
	// even when the engine uses conv.Issue.Metadata instead of extIssue.Metadata.
	if ti.Metadata != nil {
		if bp, ok := ti.Metadata["beads_priority"]; ok {
			meta["beads_priority"] = bp
		}
	}
	if len(meta) > 0 {
		raw, err := json.Marshal(meta)
		if err == nil {
			issue.Metadata = json.RawMessage(raw)
		}
	}

	return &tracker.IssueConversion{Issue: issue, Dependencies: ExtractLinkDeps(wi)}
}

// IssueToTracker converts a beads Issue to a map of ADO work item field values.
func (m *adoFieldMapper) IssueToTracker(issue *types.Issue) map[string]interface{} {
	fields := map[string]interface{}{
		FieldTitle:    truncateTitle(issue.Title),
		FieldState:    m.StatusToTracker(issue.Status),
		FieldPriority: m.PriorityToTracker(issue.Priority),
	}

	// Convert description from Markdown to HTML.
	if issue.Description != "" {
		htmlDesc, err := MarkdownToHTML(issue.Description)
		if err == nil && htmlDesc != "" {
			fields[FieldDescription] = htmlDesc
		}
	}

	// Build tags: user labels + internal beads tags for round-trip fidelity.
	tags := append([]string{}, issue.Labels...)
	if issue.Status == types.StatusBlocked {
		tags = append(tags, "beads:blocked")
	}
	// Preserve the exact beads priority via a tag for lossy mappings (beads 3 and
	// 4 both map to ADO priority 4). Tags survive the ADO round-trip through
	// FieldTags, unlike issue.Metadata which is never sent to ADO — so this tag
	// is the channel that actually lets IssueToBeads recover the original
	// priority on a real pull. (The metadata copy below is kept for callers that
	// synthesize a TrackerIssue with beads_priority, and as a local record.)
	if issue.Priority == 3 || issue.Priority == 4 {
		tags = append(tags, beadsPriorityTag(issue.Priority))
	}
	if len(tags) > 0 {
		fields[FieldTags] = buildTagString(tags)
	}

	// Store original beads priority in metadata for lossy mappings (beads 3 and 4
	// both map to ADO priority 4). This does NOT reach ADO (see the tag above),
	// but preserves the value for synthesized-TrackerIssue callers and keeps the
	// local issue.Metadata record.
	if issue.Priority == 3 || issue.Priority == 4 {
		var meta map[string]interface{}
		if len(issue.Metadata) > 0 {
			_ = json.Unmarshal(issue.Metadata, &meta)
		}
		if meta == nil {
			meta = make(map[string]interface{})
		}
		meta["beads_priority"] = strconv.Itoa(issue.Priority)
		if raw, err := json.Marshal(meta); err == nil {
			issue.Metadata = json.RawMessage(raw)
		}
	}

	// Push assignee: beads Owner (a git-author email) → System.AssignedTo
	// (beads-eotj). ADO resolves an email/unique-name to an identity
	// server-side (like Linear), so unlike github/gitlab/jira (beads-bqeq, which
	// need a user-ID lookup the code lacks) ADO CAN round-trip the assignee.
	// A non-member email would 400-reject the whole patch, so the client
	// (CreateWorkItem/UpdateWorkItem) retries WITHOUT this field on such a 400 —
	// the assignee is dropped, the rest of the push still lands.
	if issue.Owner != "" {
		fields[FieldAssignedTo] = issue.Owner
	}

	// Set Severity for Bug-type work items (required by ADO).
	// This is set before restoreMetadata so that a severity value previously
	// pulled from ADO (stored in metadata) takes precedence over the computed one.
	typeName, _ := m.TypeToTracker(issue.IssueType).(string)
	if strings.EqualFold(typeName, "Bug") {
		fields[FieldSeverity] = m.SeverityForBug(issue.Priority)
	}

	// Restore ADO-specific metadata if present (may override computed severity).
	restoreMetadata(issue, fields)

	return fields
}

// truncateTitle caps a title at ADO's System.Title limit (maxTitleLength),
// appending an ellipsis marker so the truncation is visible on the ADO side
// (beads-z5ys). A local beads title may be up to 500 chars, but ADO 400-rejects
// anything over maxTitleLength — so the PUSHED copy is truncated while the local
// title is left untouched. Rune-aware so a multi-byte character is never split.
func truncateTitle(title string) string {
	if len(title) <= maxTitleLength {
		return title
	}
	const marker = "..."
	runes := []rune(title)
	// Fast path: if the rune count already fits, the byte-length overflow was
	// from multi-byte runes — but ADO's limit is on characters, so a title whose
	// rune count is within the cap is fine as-is.
	if len(runes) <= maxTitleLength {
		return title
	}
	keep := maxTitleLength - len([]rune(marker))
	if keep < 0 {
		keep = 0
	}
	return string(runes[:keep]) + marker
}

// extractAssignedTo extracts the display name from an ADO AssignedTo field.
// The field may be a simple string or an identity object with a displayName key.
func extractAssignedTo(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]interface{}); ok {
		if name, ok := m["displayName"].(string); ok {
			return name
		}
	}
	return ""
}

// buildExternalRef constructs the ADO web URL for a work item.
// Falls back to the API URL if org/project cannot be determined.
func buildExternalRef(wi *WorkItem) string {
	if wi.URL == "" {
		return ""
	}
	// ADO API URL format: https://dev.azure.com/{org}/{project}/_apis/wit/workItems/{id}
	// Web URL format:     https://dev.azure.com/{org}/{project}/_workitems/edit/{id}
	if idx := strings.Index(wi.URL, "/_apis/"); idx > 0 {
		return fmt.Sprintf("%s/_workitems/edit/%d", wi.URL[:idx], wi.ID)
	}
	return wi.URL
}

// buildMetadata extracts ADO-specific fields into a metadata map.
func buildMetadata(wi *WorkItem) map[string]interface{} {
	meta := make(map[string]interface{})

	if v := wi.GetStringField(FieldAreaPath); v != "" {
		meta["ado.area_path"] = v
	}
	if v := wi.GetStringField(FieldIterationPath); v != "" {
		meta["ado.iteration_path"] = v
	}
	if v := wi.GetField(FieldStoryPoints); v != nil {
		meta["ado.story_points"] = v
	}
	if v := wi.GetField(FieldRemainingWork); v != nil {
		meta["ado.remaining_work"] = v
	}
	if v := wi.GetStringField(FieldSeverity); v != "" {
		meta["ado.severity"] = v
	}
	if wi.Rev > 0 {
		meta["ado.rev"] = wi.Rev
	}

	return meta
}

// restoreMetadata copies ADO-specific fields from issue metadata back into the field map.
func restoreMetadata(issue *types.Issue, fields map[string]interface{}) {
	if len(issue.Metadata) == 0 {
		return
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(issue.Metadata, &meta); err != nil {
		return
	}
	if v, ok := meta["ado.area_path"]; ok {
		fields[FieldAreaPath] = v
	}
	if v, ok := meta["ado.iteration_path"]; ok {
		fields[FieldIterationPath] = v
	}
	if v, ok := meta["ado.story_points"]; ok {
		fields[FieldStoryPoints] = v
	}
	if v, ok := meta["ado.severity"]; ok {
		fields[FieldSeverity] = v
	}
}

// validateADOTags fails loud, before any ADO API call, if a label contains a
// semicolon. ADO work-item Tags are semicolon-delimited: buildTagString joins
// with "; " and parseTags splits on ";", so a label like "status;done" would be
// silently SPLIT into two tags on push and round-trip back as two separate
// labels — corrupting the label as a dedup/round-trip identity token. beads
// labels are only edge-trimmed (utils.NormalizeLabels), so "a;b" is legal
// locally but lossy against ADO. There is no lossless transform (dropping or
// escaping the ";" would drift on pull-back and break dedup), so per the
// reusable SCM per-field-constraint class the correct behavior is to reject
// with a beads-side message naming the offending label. Mirrors the Jira
// label-whitespace guard (beads-xcbd, validateJiraLabels) and the Notion comma
// guard (beads-i8gh, validateNotionLabels). ADO-specific: ADO Tags DO allow
// spaces (unlike Jira), so only the ";" delimiter is rejected. beads-pcz2.
//
// It also rejects labels carrying the reserved "beads:" prefix. ADO round-trip
// smuggles beads' own control state through FieldTags as "beads:*" tags
// (beads:blocked, beads:priority:N — see IssueToTracker). A user label with the
// same prefix is indistinguishable from those markers and corrupts the
// round-trip: filterBeadsTags SILENTLY DROPS any "beads:"-prefixed tag on pull,
// and priorityFromTags lets a "beads:priority:N" label HIJACK the issue's
// priority. The prefix check mirrors filterBeadsTags's HasPrefix semantics
// exactly (so a normal label like "team:beads" or "status:in_progress" is never
// false-rejected). Distinct axis from the semicolon delimiter but the same SCM
// per-field-constraint fail-loud class. beads-sdmy.
func validateADOTags(labels []string) error {
	for _, l := range labels {
		if strings.Contains(l, ";") {
			return fmt.Errorf("label %q contains a semicolon; ADO rejects it (a semicolon is the work-item Tags delimiter) — rename or remove the semicolon before syncing", l)
		}
		if strings.HasPrefix(l, "beads:") {
			return fmt.Errorf("label %q uses the reserved \"beads:\" prefix; ADO round-trip reserves \"beads:*\" tags for internal state (blocked/priority) and would silently drop or misread it — rename the label before syncing", l)
		}
	}
	return nil
}

// parseTags splits an ADO semicolon-separated tag string into a trimmed slice.
// Returns nil for empty input.
func parseTags(tagStr string) []string {
	if strings.TrimSpace(tagStr) == "" {
		return nil
	}
	parts := strings.Split(tagStr, ";")
	var tags []string
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// buildTagString joins tags with "; " separator (ADO convention).
func buildTagString(tags []string) string {
	return strings.Join(tags, "; ")
}

// filterBeadsTags removes internal beads:* tags from a tag slice.
func filterBeadsTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if !strings.HasPrefix(t, "beads:") {
			out = append(out, t)
		}
	}
	return out
}

// hasBeadsTag checks if a specific beads:* tag is present in an ADO tag string.
func hasBeadsTag(tagStr, tag string) bool {
	for _, t := range parseTags(tagStr) {
		if t == tag {
			return true
		}
	}
	return false
}

// beadsPriorityTagPrefix is the marker for the round-trip priority tag.
const beadsPriorityTagPrefix = "beads:priority:"

// beadsPriorityTag builds the tag that preserves an exact beads priority through
// the ADO round-trip (e.g. priority 4 → "beads:priority:4"). Used only for the
// lossy priorities (3 and 4) that both collapse to ADO priority 4.
func beadsPriorityTag(priority int) string {
	return beadsPriorityTagPrefix + strconv.Itoa(priority)
}

// priorityFromTags recovers the exact beads priority from a beads:priority:N tag,
// if present and valid (0-4). Returns ok=false when no such tag exists.
func priorityFromTags(tags []string) (int, bool) {
	for _, t := range tags {
		if !strings.HasPrefix(t, beadsPriorityTagPrefix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(t, beadsPriorityTagPrefix))
		if err != nil || n < 0 || n > 4 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// parseBeadsPriorityValue coerces a beads_priority value carried in tracker
// metadata (string, float64, or json.Number) into a validated priority (0-4).
func parseBeadsPriorityValue(bp interface{}) (int, bool) {
	var p int
	var valid bool
	switch v := bp.(type) {
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			p, valid = n, true
		}
	case float64:
		p, valid = int(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			p, valid = int(n), true
		}
	}
	if valid && p >= 0 && p <= 4 {
		return p, true
	}
	return 0, false
}
