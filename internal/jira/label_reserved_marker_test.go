package jira

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestValidateJiraLabelsRejectsDeferredMarker is the teeth for beads-nqu1:
// Jira round-trip preserves beads' "deferred" status through the labels array
// as a reserved "bd:status:deferred" marker (see beadsDeferredLabel). User
// labels are pushed into the SAME array, so a user label equal to that marker
// collides with it and corrupts the round-trip two ways:
//   - stripLabel removes it on pull, so a legitimate user label
//     "bd:status:deferred" is SILENTLY DROPPED.
//   - hasLabel recognizes it, so the label HIJACKS the issue's status to
//     deferred.
//
// It also has no whitespace, so it passes the existing whitespace guard
// (beads-xcbd). Jira sibling of the ADO "beads:" (beads-sdmy) and GitLab
// "bd:estimate:" (beads-yl92) reserved-marker-in-label collision axis. Jira's
// hasLabel/stripLabel are EXACT-match (not HasPrefix), so the guard rejects
// only the exact marker string, not a prefix family.
func TestValidateJiraLabelsRejectsDeferredMarker(t *testing.T) {
	err := validateJiraLabels([]string{"backend", beadsDeferredLabel, "urgent"})
	if err == nil {
		t.Fatalf("validateJiraLabels(reserved marker %q) = nil, want error", beadsDeferredLabel)
	}
	if !strings.Contains(err.Error(), beadsDeferredLabel) {
		t.Errorf("error %q should name the offending label %q", err.Error(), beadsDeferredLabel)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "reserved") {
		t.Errorf("error %q should explain the reserved-marker constraint", err.Error())
	}
}

// TestValidateJiraLabelsAcceptsNearMissMarkers confirms labels that merely
// resemble the reserved marker WITHOUT equaling it stay legal — the guard is
// exact-match (mirroring hasLabel/stripLabel), so it cannot false-reject.
func TestValidateJiraLabelsAcceptsNearMissMarkers(t *testing.T) {
	for _, labels := range [][]string{
		{"bd:status:deferredx"},
		{"bd:status:open"},
		{"bd:status:deferre"},
		{"status:deferred"},
	} {
		if err := validateJiraLabels(labels); err != nil {
			t.Errorf("validateJiraLabels(%v) = %v, want nil", labels, err)
		}
	}
}

// TestCreateIssueRejectsDeferredMarkerPreflight is integration-level teeth: the
// push path (CreateIssue) must fail loud BEFORE any Jira API call when a label
// equals the reserved deferred marker.
func TestCreateIssueRejectsDeferredMarkerPreflight(t *testing.T) {
	tr := &Tracker{}
	issue := &types.Issue{
		Title:     "A valid title",
		IssueType: types.TypeBug,
		Labels:    []string{"backend", beadsDeferredLabel},
	}

	_, err := tr.CreateIssue(nil, issue)
	if err == nil {
		t.Fatal("CreateIssue with the reserved deferred marker = nil error, want pre-flight rejection")
	}
	if !strings.Contains(err.Error(), beadsDeferredLabel) {
		t.Errorf("CreateIssue error %q should name the offending label", err.Error())
	}
}
