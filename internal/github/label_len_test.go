package github

import (
	"strings"
	"testing"
)

// TestValidateGitHubLabels_RejectsOverCap is the teeth for beads-98gh: GitHub
// caps an issue LABEL NAME at maxLabelNameLength characters and 422-rejects a
// create/update whose label overflows it, failing the WHOLE issue push with an
// opaque error. A beads label is legal locally up to 255 chars (types Validate),
// so a 51-255 char label is legal locally but unpushable to GitHub. Per the
// class ruling (beads-xcbd) a label is an identity token used for dedup and
// round-trip, so the guard REJECTS (fails loud, naming the label) rather than
// truncating — truncating would drift on pull-back and break dedup. Mirrors the
// jira label-whitespace guard (validateJiraLabels).
func TestValidateGitHubLabels_RejectsOverCap(t *testing.T) {
	over := strings.Repeat("a", maxLabelNameLength+1)
	err := validateGitHubLabels([]string{"backend", over, "urgent"})
	if err == nil {
		t.Fatal("validateGitHubLabels(over-cap label) = nil, want error")
	}
	// The error must name the offending label so the operator can fix it.
	if !strings.Contains(err.Error(), over) {
		t.Errorf("error does not name the offending label: %v", err)
	}
}

// TestValidateGitHubLabels_AllowsWithinCap confirms labels at or under the cap
// pass — including a label exactly at maxLabelNameLength (boundary).
func TestValidateGitHubLabels_AllowsWithinCap(t *testing.T) {
	exact := strings.Repeat("b", maxLabelNameLength)
	labels := []string{"backend", "needs-review", exact}
	if err := validateGitHubLabels(labels); err != nil {
		t.Errorf("validateGitHubLabels(%v) = %v, want nil", labels, err)
	}
	// Empty / nil is a no-op.
	if err := validateGitHubLabels(nil); err != nil {
		t.Errorf("validateGitHubLabels(nil) = %v, want nil", err)
	}
}

// TestValidateGitHubLabels_RuneAware confirms the cap counts characters (runes),
// not bytes: a label of maxLabelNameLength multi-byte runes is within the cap
// even though its byte length overflows.
func TestValidateGitHubLabels_RuneAware(t *testing.T) {
	// maxLabelNameLength multi-byte runes (each 'é' is 2 bytes) is within the
	// character cap — GitHub counts characters.
	multibyte := strings.Repeat("é", maxLabelNameLength)
	if err := validateGitHubLabels([]string{multibyte}); err != nil {
		t.Errorf("validateGitHubLabels(%d-rune multibyte label) = %v, want nil", maxLabelNameLength, err)
	}
	// One rune over the cap must reject.
	over := strings.Repeat("é", maxLabelNameLength+1)
	if err := validateGitHubLabels([]string{over}); err == nil {
		t.Error("validateGitHubLabels(over-cap multibyte label) = nil, want error")
	}
}
