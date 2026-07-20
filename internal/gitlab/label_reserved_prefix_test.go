package gitlab

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestValidateGitLabLabelsRejectsEstimatePrefix is the teeth for beads-yl92:
// GitLab round-trip smuggles beads' exact EstimatedMinutes through the label
// array as a reserved "bd:estimate:<minutes>" marker (see beadsEstimateLabelPrefix).
// User labels are pushed into the SAME array, so a user label carrying the
// reserved "bd:estimate:" prefix is indistinguishable from the internal marker
// and corrupts the round-trip two ways:
//   - stripEstimateLabels drops any "bd:estimate:"-prefixed label on pull, so a
//     legitimate user label "bd:estimate:120" is SILENTLY DROPPED.
//   - estimateFromLabels reads any "bd:estimate:N" label, so a user label
//     "bd:estimate:120" HIJACKS the issue's EstimatedMinutes.
//
// GitLab sibling of the ADO "beads:" prefix guard (beads-sdmy) — the same SCM
// per-field-constraint reserved-prefix collision axis. (The priority::/status::/
// type:: scoped labels are GitLab's OWN native convention and are intentionally
// bidirectional, so they are NOT rejected.)
func TestValidateGitLabLabelsRejectsEstimatePrefix(t *testing.T) {
	for _, label := range []string{"bd:estimate:120", "bd:estimate:", "bd:estimate:abc"} {
		err := validateGitLabLabels([]string{"backend", label, "urgent"})
		if err == nil {
			t.Fatalf("validateGitLabLabels(reserved label %q) = nil, want error", label)
		}
		if !strings.Contains(err.Error(), label) {
			t.Errorf("error %q should name the offending label %q", err.Error(), label)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "reserved") {
			t.Errorf("error %q should explain the reserved-prefix constraint", err.Error())
		}
	}
}

// TestValidateGitLabLabelsAcceptsCleanLabels confirms labels that do not carry
// the reserved "bd:estimate:" prefix stay legal — including GitLab-native scoped
// labels (priority::/status::/type::, intentionally bidirectional), labels that
// merely contain "estimate", and near-miss prefixes. The guard matches
// stripEstimateLabels's HasPrefix semantics exactly, so it never false-rejects.
func TestValidateGitLabLabelsAcceptsCleanLabels(t *testing.T) {
	for _, labels := range [][]string{
		nil,
		{},
		{"backend"},
		{"priority::high", "status::blocked", "type::bug"},
		{"my-estimate:5", "estimate", "bd-estimate:5"},
		{"bd:estimatex:5", "bd:priority:2"},
	} {
		if err := validateGitLabLabels(labels); err != nil {
			t.Errorf("validateGitLabLabels(%v) = %v, want nil", labels, err)
		}
	}
}

// TestCreateIssueRejectsEstimatePrefixLabelPreflight is integration-level teeth:
// the push path (CreateIssue) must fail loud BEFORE any GitLab API call when a
// label carries the reserved "bd:estimate:" prefix. The nil client would panic
// if the push reached an API call.
func TestCreateIssueRejectsEstimatePrefixLabelPreflight(t *testing.T) {
	tr := &Tracker{}
	issue := &types.Issue{
		Title:     "A valid title",
		IssueType: types.TypeBug,
		Labels:    []string{"backend", "bd:estimate:60"},
	}

	_, err := tr.CreateIssue(nil, issue)
	if err == nil {
		t.Fatal("CreateIssue with the reserved estimate-prefix label = nil error, want pre-flight rejection")
	}
	if !strings.Contains(err.Error(), "bd:estimate:60") {
		t.Errorf("CreateIssue error %q should name the offending label", err.Error())
	}
}
