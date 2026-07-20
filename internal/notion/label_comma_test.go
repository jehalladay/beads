package notion

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestValidateNotionLabelsRejectsComma is the teeth for beads-i8gh:
// Notion 400-rejects a multi_select option name containing a comma (the comma
// is Notion's option-name delimiter). beads labels are only edge-trimmed
// (utils.NormalizeLabels), so "backend,urgent" is legal locally but would fail
// the whole Notion page push with an opaque API 400. Mirrors the jira
// label-whitespace guard (beads-xcbd, validateJiraLabels): (c)
// reject-with-clear-error, pre-flight — fail loud BEFORE the API call with a
// beads-side message naming the offending label.
func TestValidateNotionLabelsRejectsComma(t *testing.T) {
	err := validateNotionLabels([]string{"backend", "needs,review", "urgent"})
	if err == nil {
		t.Fatal("validateNotionLabels(comma-bearing label) = nil, want error")
	}
	// The message must name the offending label so the user can fix it.
	if !strings.Contains(err.Error(), "needs,review") {
		t.Errorf("error %q should name the offending label 'needs,review'", err.Error())
	}
	// It must be a beads-side validation error explaining the comma constraint.
	if !strings.Contains(strings.ToLower(err.Error()), "comma") {
		t.Errorf("error %q should explain the comma constraint", err.Error())
	}
}

// TestValidateNotionLabelsAcceptsCleanLabels confirms comma-free labels pass.
func TestValidateNotionLabelsAcceptsCleanLabels(t *testing.T) {
	for _, labels := range [][]string{
		nil,
		{},
		{"backend"},
		{"backend", "urgent", "p1"},
		{"status:in_progress", "type-bug", "needs review"},
	} {
		if err := validateNotionLabels(labels); err != nil {
			t.Errorf("validateNotionLabels(%v) = %v, want nil", labels, err)
		}
	}
}

// TestCreateIssueRejectsCommaLabelPreflight is an integration-level teeth: the
// push path (CreateIssue) must fail loud BEFORE any Notion API call when a
// label carries a comma, returning the named validation error.
func TestCreateIssueRejectsCommaLabelPreflight(t *testing.T) {
	tr := &Tracker{}
	issue := &types.Issue{
		Title:     "A valid title",
		IssueType: types.TypeBug,
		Labels:    []string{"backend", "a,b"},
	}

	_, err := tr.CreateIssue(nil, issue)
	if err == nil {
		t.Fatal("CreateIssue with a comma-bearing label = nil error, want pre-flight rejection")
	}
	if !strings.Contains(err.Error(), "a,b") {
		t.Errorf("CreateIssue error %q should name the offending label", err.Error())
	}
}
