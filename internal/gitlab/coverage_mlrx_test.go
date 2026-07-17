package gitlab

import (
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// StatusToBeads with an empty StateMap must fall through to the GitLab-specific
// switch (opened/reopened -> open, closed -> closed); the existing tests always
// hit the StateMap, leaving the switch arms uncovered.
func TestFieldMapperStatusToBeadsSwitchFallback(t *testing.T) {
	m := &gitlabFieldMapper{config: &MappingConfig{StateMap: map[string]string{}}}

	cases := []struct {
		in   string
		want types.Status
	}{
		{"opened", types.StatusOpen},
		{"reopened", types.StatusOpen},
		{"closed", types.StatusClosed},
		{"unknown-state", types.StatusOpen}, // string, unmapped, not in switch -> default
	}
	for _, c := range cases {
		if got := m.StatusToBeads(c.in); got != c.want {
			t.Errorf("StatusToBeads(%q) switch fallback = %q, want %q", c.in, got, c.want)
		}
	}

	// Non-string input still returns the StatusOpen default.
	if got := m.StatusToBeads(nil); got != types.StatusOpen {
		t.Errorf("StatusToBeads(nil) = %q, want %q", got, types.StatusOpen)
	}
}

// BuildExternalRef with no URL falls back to the "gitlab:<identifier>" form.
func TestBuildExternalRefIdentifierFallback(t *testing.T) {
	tr := &Tracker{}
	ti := &tracker.TrackerIssue{Identifier: "42"}
	if ref := tr.BuildExternalRef(ti); ref != "gitlab:42" {
		t.Errorf("BuildExternalRef() = %q, want %q", ref, "gitlab:42")
	}
}
