package jira

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestValidateJiraLabelsRejectsInternalSpace is the teeth for beads-xcbd:
// Jira 400-rejects a label containing internal whitespace (Jira labels are
// single-token). beads labels are only edge-trimmed (utils.NormalizeLabels),
// so "needs review" is legal locally but would fail the Jira push with an
// opaque API 400. PM ruled (c) reject-with-clear-error, pre-flight: fail loud
// BEFORE the API call with a beads-side message naming the offending label.
func TestValidateJiraLabelsRejectsInternalSpace(t *testing.T) {
	err := validateJiraLabels([]string{"backend", "needs review", "urgent"})
	if err == nil {
		t.Fatal("validateJiraLabels(space-bearing label) = nil, want error")
	}
	// The message must name the offending label so the user can fix it.
	if !strings.Contains(err.Error(), "needs review") {
		t.Errorf("error %q should name the offending label 'needs review'", err.Error())
	}
	// It must be a beads-side validation error, not a passthrough Jira 400.
	if !strings.Contains(strings.ToLower(err.Error()), "whitespace") &&
		!strings.Contains(strings.ToLower(err.Error()), "space") {
		t.Errorf("error %q should explain the whitespace constraint", err.Error())
	}
}

// TestValidateJiraLabelsAcceptsCleanLabels confirms single-token labels
// (including tab-free scoped-style labels) pass without error.
func TestValidateJiraLabelsAcceptsCleanLabels(t *testing.T) {
	for _, labels := range [][]string{
		nil,
		{},
		{"backend"},
		{"backend", "urgent", "p1"},
		{"status:in_progress", "type-bug"},
	} {
		if err := validateJiraLabels(labels); err != nil {
			t.Errorf("validateJiraLabels(%v) = %v, want nil", labels, err)
		}
	}
}

// TestValidateJiraLabelsRejectsTabAndNewline confirms the guard catches ALL
// whitespace (tab, newline), not just the space rune — Jira rejects any
// internal whitespace in a label.
func TestValidateJiraLabelsRejectsTabAndNewline(t *testing.T) {
	for _, bad := range []string{"a\tb", "a\nb", "a b"} {
		if err := validateJiraLabels([]string{bad}); err == nil {
			t.Errorf("validateJiraLabels([%q]) = nil, want error (internal whitespace)", bad)
		}
	}
}

// TestCreateIssueRejectsSpaceLabelPreflight is an integration-level teeth: the
// push path (CreateIssue) must fail loud BEFORE any Jira API call when a label
// carries internal whitespace, returning the named validation error.
func TestCreateIssueRejectsSpaceLabelPreflight(t *testing.T) {
	tr := &Tracker{}
	issue := &types.Issue{
		Title:     "A valid title",
		IssueType: types.TypeBug,
		Labels:    []string{"backend", "needs review"},
	}

	_, err := tr.CreateIssue(nil, issue)
	if err == nil {
		t.Fatal("CreateIssue with a space-bearing label = nil error, want pre-flight rejection")
	}
	if !strings.Contains(err.Error(), "needs review") {
		t.Errorf("CreateIssue error %q should name the offending label", err.Error())
	}
}
