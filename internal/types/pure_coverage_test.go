package types

import "testing"

// beads-0a65: hermetic table tests for pure type helpers that were at 0%:
// ExtractPrefix, IssueType.Normalize (alias arms), WispType.IsValid,
// WorkType.IsValid, and DependencyType.IsBlockingEdge.

func TestExtractPrefix(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"beads-123", "beads-"},
		{"bd-1", "bd-"},
		{"a-b-c", "a-"},   // stops at the first hyphen
		{"noHyphen", ""},  // no hyphen → empty
		{"", ""},          // empty input
		{"-leading", "-"}, // hyphen at index 0 → "-"
		{"trailing-", "trailing-"},
	}
	for _, tt := range tests {
		if got := ExtractPrefix(tt.id); got != tt.want {
			t.Errorf("ExtractPrefix(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestIssueTypeNormalize(t *testing.T) {
	tests := []struct {
		in   IssueType
		want IssueType
	}{
		// Documented aliases (case-insensitive).
		{"enhancement", TypeFeature},
		{"feat", TypeFeature},
		{"Enhancement", TypeFeature},
		{"dec", TypeDecision},
		{"adr", TypeDecision},
		{"ADR", TypeDecision},
		{"investigation", TypeSpike},
		{"timebox", TypeSpike},
		{"user-story", TypeStory},
		{"user_story", TypeStory},
		{"ms", TypeMilestone},
		// Canonical types pass through unchanged.
		{TypeBug, TypeBug},
		{TypeTask, TypeTask},
		{TypeFeature, TypeFeature},
		// Unknown values are returned as-is (no forced canonicalization).
		{"widget", "widget"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := tt.in.Normalize(); got != tt.want {
			t.Errorf("IssueType(%q).Normalize() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWispTypeIsValid(t *testing.T) {
	valid := []WispType{
		WispTypeHeartbeat, WispTypePing, WispTypePatrol, WispTypeGCReport,
		WispTypeRecovery, WispTypeError, WispTypeEscalation,
		"", // empty is valid (uses default TTL)
	}
	for _, w := range valid {
		if !w.IsValid() {
			t.Errorf("WispType(%q).IsValid() = false, want true", w)
		}
	}
	invalid := []WispType{"bogus", "heartbeats", "gc-report", "ESCALATION"}
	for _, w := range invalid {
		if w.IsValid() {
			t.Errorf("WispType(%q).IsValid() = true, want false", w)
		}
	}
}

func TestWorkTypeIsValid(t *testing.T) {
	valid := []WorkType{WorkTypeMutex, WorkTypeOpenCompetition, ""}
	for _, w := range valid {
		if !w.IsValid() {
			t.Errorf("WorkType(%q).IsValid() = false, want true", w)
		}
	}
	invalid := []WorkType{"bogus", "MUTEX", "open competition", "openCompetition"}
	for _, w := range invalid {
		if w.IsValid() {
			t.Errorf("WorkType(%q).IsValid() = true, want false", w)
		}
	}
}

func TestDependencyTypeIsBlockingEdge(t *testing.T) {
	blocking := []DependencyType{DepBlocks, DepConditionalBlocks, DepWaitsFor}
	for _, d := range blocking {
		if !d.IsBlockingEdge() {
			t.Errorf("DependencyType(%q).IsBlockingEdge() = false, want true", d)
		}
	}
	nonBlocking := []DependencyType{
		DepParentChild, DepRelated, DepDiscoveredFrom, DepTracks,
		DepRepliesTo, DepRelatesTo, DepDuplicates, DepSupersedes,
		DepUntil, DepCausedBy, DepValidates, "custom", "",
	}
	for _, d := range nonBlocking {
		if d.IsBlockingEdge() {
			t.Errorf("DependencyType(%q).IsBlockingEdge() = true, want false", d)
		}
	}
}
