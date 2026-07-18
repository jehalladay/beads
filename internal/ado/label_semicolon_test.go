package ado

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestValidateADOTagsRejectsSemicolon is the teeth for beads-pcz2:
// ADO work-item Tags are semicolon-delimited (buildTagString joins with "; ",
// parseTags splits on ";"). beads labels are only edge-trimmed
// (utils.NormalizeLabels), so a label like "status;done" is legal locally but
// would be silently SPLIT into two ADO tags on push and round-trip back as two
// separate labels — corrupting the label as a dedup/round-trip identity token.
// Mirrors the Jira label-whitespace guard (beads-xcbd, validateJiraLabels) and
// the Notion comma guard (beads-i8gh, validateNotionLabels): the reusable SCM
// per-field-constraint class — reject-with-clear-error, pre-flight, naming the
// offending label rather than silently splitting it.
func TestValidateADOTagsRejectsSemicolon(t *testing.T) {
	err := validateADOTags([]string{"backend", "status;done", "urgent"})
	if err == nil {
		t.Fatal("validateADOTags(semicolon-bearing label) = nil, want error")
	}
	// The message must name the offending label so the user can fix it.
	if !strings.Contains(err.Error(), "status;done") {
		t.Errorf("error %q should name the offending label 'status;done'", err.Error())
	}
	// It must be a beads-side validation error explaining the semicolon constraint.
	if !strings.Contains(strings.ToLower(err.Error()), "semicolon") {
		t.Errorf("error %q should explain the semicolon constraint", err.Error())
	}
}

// TestValidateADOTagsAcceptsCleanLabels confirms semicolon-free labels pass,
// including labels with spaces (ADO Tags allow spaces — only ";" is the
// delimiter, unlike Jira which rejects whitespace).
func TestValidateADOTagsAcceptsCleanLabels(t *testing.T) {
	for _, labels := range [][]string{
		nil,
		{},
		{"backend"},
		{"backend", "urgent", "p1"},
		{"needs review", "type-bug", "status:in_progress"},
	} {
		if err := validateADOTags(labels); err != nil {
			t.Errorf("validateADOTags(%v) = %v, want nil", labels, err)
		}
	}
}

// TestCreateIssueRejectsSemicolonLabelPreflight is an integration-level teeth:
// the push path (CreateIssue) must fail loud BEFORE any ADO API call when a
// label carries a semicolon, returning the named validation error. The nil
// client would panic if the push reached an API call.
func TestCreateIssueRejectsSemicolonLabelPreflight(t *testing.T) {
	tr := &Tracker{}
	issue := &types.Issue{
		Title:     "A valid title",
		IssueType: types.TypeBug,
		Labels:    []string{"backend", "a;b"},
	}

	_, err := tr.CreateIssue(nil, issue)
	if err == nil {
		t.Fatal("CreateIssue with a semicolon-bearing label = nil error, want pre-flight rejection")
	}
	if !strings.Contains(err.Error(), "a;b") {
		t.Errorf("CreateIssue error %q should name the offending label", err.Error())
	}
}
