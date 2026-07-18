package ado

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestValidateADOTagsRejectsReservedPrefix is the teeth for beads-sdmy:
// ADO round-trip smuggles beads' own control state through FieldTags as reserved
// "beads:*" tags (beads:blocked, beads:priority:N). User labels are appended into
// the SAME tag string, so a user label carrying the reserved "beads:" prefix is
// indistinguishable from the internal markers and corrupts the round-trip two ways:
//   - filterBeadsTags strips ALL "beads:"-prefixed tags on pull, so a legitimate
//     user label "beads:blocked" is SILENTLY DROPPED.
//   - priorityFromTags reads any "beads:priority:N" tag, so a user label
//     "beads:priority:0" HIJACKS the issue's priority.
//
// Distinct axis from the semicolon delimiter guard (beads-pcz2) but the same SCM
// per-field-constraint fail-loud class — reject pre-flight, naming the label.
func TestValidateADOTagsRejectsReservedPrefix(t *testing.T) {
	for _, label := range []string{"beads:blocked", "beads:priority:0", "beads:anything"} {
		err := validateADOTags([]string{"backend", label, "urgent"})
		if err == nil {
			t.Fatalf("validateADOTags(reserved label %q) = nil, want error", label)
		}
		if !strings.Contains(err.Error(), label) {
			t.Errorf("error %q should name the offending label %q", err.Error(), label)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "reserved") {
			t.Errorf("error %q should explain the reserved-prefix constraint", err.Error())
		}
	}
}

// TestValidateADOTagsAcceptsNonReservedColonLabels confirms labels that merely
// contain a colon or the substring "beads" WITHOUT the reserved "beads:" prefix
// stay legal — the guard matches filterBeadsTags's HasPrefix semantics exactly,
// not a loose Contains, so it cannot false-reject normal labels.
func TestValidateADOTagsAcceptsNonReservedColonLabels(t *testing.T) {
	for _, labels := range [][]string{
		{"status:in_progress"},
		{"team:beads"},
		{"beads-cli"},
		{"my-beads:thing"},
		{"beadsx:foo"},
	} {
		if err := validateADOTags(labels); err != nil {
			t.Errorf("validateADOTags(%v) = %v, want nil", labels, err)
		}
	}
}

// TestCreateIssueRejectsReservedPrefixLabelPreflight is integration-level teeth:
// the push path (CreateIssue) must fail loud BEFORE any ADO API call when a label
// carries the reserved "beads:" prefix. The nil client would panic if the push
// reached an API call.
func TestCreateIssueRejectsReservedPrefixLabelPreflight(t *testing.T) {
	tr := &Tracker{}
	issue := &types.Issue{
		Title:     "A valid title",
		IssueType: types.TypeBug,
		Labels:    []string{"backend", "beads:priority:1"},
	}

	_, err := tr.CreateIssue(nil, issue)
	if err == nil {
		t.Fatal("CreateIssue with a reserved-prefix label = nil error, want pre-flight rejection")
	}
	if !strings.Contains(err.Error(), "beads:priority:1") {
		t.Errorf("CreateIssue error %q should name the offending label", err.Error())
	}
}
