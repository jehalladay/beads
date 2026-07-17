package gitlab

import (
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

func newTestFieldMapper() *gitlabFieldMapper {
	return &gitlabFieldMapper{config: DefaultMappingConfig()}
}

// TestFieldMapperPriorityToBeads covers label→numeric mapping including the
// default fallback for unrecognized/non-string inputs.
func TestFieldMapperPriorityToBeads(t *testing.T) {
	t.Parallel()
	m := newTestFieldMapper()
	cases := []struct {
		in   interface{}
		want int
	}{
		{"critical", 0},
		{"high", 1},
		{"medium", 2},
		{"low", 3},
		{"none", 4},
		{"bogus", 2}, // unknown label -> default P2
		{42, 2},      // non-string -> default P2
		{nil, 2},     // nil -> default P2
	}
	for _, c := range cases {
		if got := m.PriorityToBeads(c.in); got != c.want {
			t.Errorf("PriorityToBeads(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestFieldMapperPriorityToTracker covers the inverse numeric→label lookup and
// the "medium" fallback for a priority absent from the map.
func TestFieldMapperPriorityToTracker(t *testing.T) {
	t.Parallel()
	m := newTestFieldMapper()

	// Known priorities must round-trip back through PriorityToBeads.
	for _, p := range []int{0, 1, 2, 3, 4} {
		label := m.PriorityToTracker(p)
		s, ok := label.(string)
		if !ok {
			t.Fatalf("PriorityToTracker(%d) = %v (%T), want string", p, label, label)
		}
		if got := m.PriorityToBeads(s); got != p {
			t.Errorf("PriorityToTracker(%d)=%q round-trips to %d, want %d", p, s, got, p)
		}
	}

	// A priority not present in the map falls back to "medium".
	if got := m.PriorityToTracker(99); got != "medium" {
		t.Errorf("PriorityToTracker(99) = %v, want \"medium\"", got)
	}
}

// TestFieldMapperStatusToBeads covers explicit StateMap hits, the GitLab
// opened/reopened/closed defaults, and the non-string/unknown fallback.
func TestFieldMapperStatusToBeads(t *testing.T) {
	t.Parallel()
	m := newTestFieldMapper()
	cases := []struct {
		in   interface{}
		want types.Status
	}{
		{"opened", types.StatusOpen},
		{"reopened", types.StatusOpen},
		{"closed", types.StatusClosed},
		{"totally-unknown", types.StatusOpen}, // string but not mapped -> default open
		{123, types.StatusOpen},               // non-string -> default open
	}
	for _, c := range cases {
		if got := m.StatusToBeads(c.in); got != c.want {
			t.Errorf("StatusToBeads(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFieldMapperStatusToTracker covers closed→"closed" and everything-else→"opened".
func TestFieldMapperStatusToTracker(t *testing.T) {
	t.Parallel()
	m := newTestFieldMapper()
	cases := []struct {
		in   types.Status
		want string
	}{
		{types.StatusClosed, "closed"},
		{types.StatusOpen, "opened"},
		{types.StatusInProgress, "opened"},
		{types.StatusBlocked, "opened"},
	}
	for _, c := range cases {
		if got := m.StatusToTracker(c.in); got != c.want {
			t.Errorf("StatusToTracker(%q) = %v, want %q", c.in, got, c.want)
		}
	}
}

// TestFieldMapperTypeToBeads covers LabelTypeMap hits, the enhancement→feature
// alias, and the default-task fallback for unknown/non-string inputs.
func TestFieldMapperTypeToBeads(t *testing.T) {
	t.Parallel()
	m := newTestFieldMapper()
	cases := []struct {
		in   interface{}
		want types.IssueType
	}{
		{"bug", types.IssueType("bug")},
		{"feature", types.IssueType("feature")},
		{"epic", types.IssueType("epic")},
		{"chore", types.IssueType("chore")},
		{"enhancement", types.IssueType("feature")}, // alias
		{"mystery", types.TypeTask},                 // unknown -> task
		{7, types.TypeTask},                         // non-string -> task
	}
	for _, c := range cases {
		if got := m.TypeToBeads(c.in); got != c.want {
			t.Errorf("TypeToBeads(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFieldMapperTypeToTracker covers the straight string passthrough.
func TestFieldMapperTypeToTracker(t *testing.T) {
	t.Parallel()
	m := newTestFieldMapper()
	if got := m.TypeToTracker(types.TypeBug); got != "bug" {
		t.Errorf("TypeToTracker(bug) = %v, want \"bug\"", got)
	}
	if got := m.TypeToTracker(types.TypeTask); got != "task" {
		t.Errorf("TypeToTracker(task) = %v, want \"task\"", got)
	}
}

// TestFieldMapperIssueToBeads covers the happy path plus the non-*Issue Raw
// early-return guard.
func TestFieldMapperIssueToBeads(t *testing.T) {
	t.Parallel()
	m := newTestFieldMapper()

	// Raw is not a *Issue -> nil.
	if got := m.IssueToBeads(&tracker.TrackerIssue{Raw: "not-an-issue"}); got != nil {
		t.Errorf("IssueToBeads(non-*Issue Raw) = %v, want nil", got)
	}

	// Happy path: a real GitLab issue converts and carries the title through.
	gl := &Issue{
		ID:          100,
		IID:         42,
		Title:       "Fix pipeline",
		Description: "CI is broken",
		State:       "opened",
		WebURL:      "https://gitlab.com/group/project/-/issues/42",
		Labels:      []string{"bug"},
	}
	conv := m.IssueToBeads(&tracker.TrackerIssue{Raw: gl})
	if conv == nil {
		t.Fatal("IssueToBeads(valid *Issue) = nil, want conversion")
	}
	if conv.Issue == nil || conv.Issue.Title != "Fix pipeline" {
		t.Errorf("IssueToBeads conversion title = %+v, want title \"Fix pipeline\"", conv.Issue)
	}
}

// TestFieldMapperIssueToTracker covers delegation to BeadsIssueToGitLabFields.
func TestFieldMapperIssueToTracker(t *testing.T) {
	t.Parallel()
	m := newTestFieldMapper()
	issue := &types.Issue{
		ID:       "bd-1",
		Title:    "Add feature",
		Priority: 1,
		Status:   types.StatusOpen,
	}
	fields := m.IssueToTracker(issue)
	if fields == nil {
		t.Fatal("IssueToTracker returned nil map")
	}
	if title, ok := fields["title"]; !ok || title != "Add feature" {
		t.Errorf("IssueToTracker fields[title] = %v, want \"Add feature\"", fields["title"])
	}
}
