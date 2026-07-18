package linear

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestStripIdempotencyMarker is the unit teeth for beads-5ahf: the round-trip
// of AppendIdempotencyMarker must be exactly reversible so the marker never
// leaks into the beads description and never desyncs the change-detection
// compare. Stripping a description that carries the marker (with the newline
// separator that AppendIdempotencyMarker adds) must yield the original body;
// stripping a marker-free description must be a no-op.
func TestStripIdempotencyMarker(t *testing.T) {
	marker := GenerateIdempotencyMarker("beads-abc", "me@example.com", 123)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"appended to non-empty body", AppendIdempotencyMarker("real body", marker), "real body"},
		{"marker is the whole body", AppendIdempotencyMarker("", marker), ""},
		{"multiline body", AppendIdempotencyMarker("line1\nline2", marker), "line1\nline2"},
		{"no marker present", "just a description", "just a description"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := StripIdempotencyMarker(c.in); got != c.want {
			t.Errorf("%s: StripIdempotencyMarker(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestIssueToBeadsStripsIdempotencyMarker is the import teeth: a Linear issue
// whose description carries the marker (because beads pushed it via the
// idempotent-create path) must NOT leak the marker into the imported beads
// description.
func TestIssueToBeadsStripsIdempotencyMarker(t *testing.T) {
	marker := GenerateIdempotencyMarker("beads-abc", "me@example.com", 123)
	li := &Issue{
		Title:       "A title",
		Description: AppendIdempotencyMarker("the real description", marker),
		CreatedAt:   "2026-01-01T00:00:00Z",
		UpdatedAt:   "2026-01-01T00:00:00Z",
	}
	conv := IssueToBeads(li, DefaultMappingConfig())
	if conv == nil || conv.Issue == nil {
		t.Fatal("IssueToBeads returned nil")
	}
	imported, ok := conv.Issue.(*types.Issue)
	if !ok {
		t.Fatalf("conv.Issue is %T, want *types.Issue", conv.Issue)
	}
	if got := imported.Description; got != "the real description" {
		t.Errorf("imported description = %q, want %q (marker must be stripped)", got, "the real description")
	}
}

// TestPushFieldsEqualIgnoresIdempotencyMarker is the compare teeth: a
// beads-originated Linear issue (whose remote description carries the marker)
// must compare EQUAL to the unchanged local issue, so the unchanged-skip check
// does not re-push it on every sync.
func TestPushFieldsEqualIgnoresIdempotencyMarker(t *testing.T) {
	config := DefaultMappingConfig()
	local := &types.Issue{
		Title:       "A title",
		Description: "the real description",
		Priority:    2,
		Status:      types.StatusOpen,
	}
	marker := GenerateIdempotencyMarker("beads-abc", "me@example.com", 123)
	remote := &Issue{
		Title:       "A title",
		Description: AppendIdempotencyMarker(BuildLinearDescription(local), marker),
		Priority:    PriorityToLinear(local.Priority, config),
		// nil State maps to StatusOpen (see StateToBeadsStatus), matching local.
		State: nil,
	}
	if !PushFieldsEqual(local, remote, config) {
		t.Error("PushFieldsEqual = false for an unchanged issue whose remote carries the idempotency marker; want true (marker must be ignored in the compare)")
	}
}
