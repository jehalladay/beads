package linear

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestStripIdempotencyMarker verifies StripIdempotencyMarker is the exact
// inverse of AppendIdempotencyMarker: it removes the bd-idempotency marker (and
// the single newline separator Append inserts before it) so a pushed
// description round-trips back to its original body. beads-5ahf.
func TestStripIdempotencyMarker(t *testing.T) {
	marker := GenerateIdempotencyMarker("bd-abc", "a@b.c", 1234567890)

	tests := []struct {
		name string
		body string
	}{
		{"non-empty body", "Fix the widget rendering bug."},
		{"multi-line body", "Line one.\nLine two.\n\nLine four."},
		{"body ending in newline", "trailing newline\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appended := AppendIdempotencyMarker(tt.body, marker)
			got := StripIdempotencyMarker(appended)
			if got != tt.body {
				t.Errorf("StripIdempotencyMarker(Append(%q)) = %q; want %q", tt.body, got, tt.body)
			}
		})
	}
}

// TestStripIdempotencyMarkerEmptyBody covers the case where Append made the
// marker the entire body (empty original description): stripping must return "".
func TestStripIdempotencyMarkerEmptyBody(t *testing.T) {
	marker := GenerateIdempotencyMarker("bd-xyz", "x@y.z", 42)
	appended := AppendIdempotencyMarker("", marker)
	if appended != marker {
		t.Fatalf("Append(\"\", marker) = %q; want the bare marker %q", appended, marker)
	}
	if got := StripIdempotencyMarker(appended); got != "" {
		t.Errorf("StripIdempotencyMarker(bare marker) = %q; want empty string", got)
	}
}

// TestStripIdempotencyMarkerNoMarker verifies a marker-free description passes
// through unchanged (no accidental mutation).
func TestStripIdempotencyMarkerNoMarker(t *testing.T) {
	body := "A description with no marker at all.\nStill none here."
	if got := StripIdempotencyMarker(body); got != body {
		t.Errorf("StripIdempotencyMarker(no marker) = %q; want %q", got, body)
	}
	if got := StripIdempotencyMarker(""); got != "" {
		t.Errorf("StripIdempotencyMarker(\"\") = %q; want empty string", got)
	}
}

// TestPushFieldsEqualIgnoresIdempotencyMarker is the teeth test for the actual
// bug: a beads-originated issue whose remote description carries the embedded
// idempotency marker must still compare EQUAL to the unchanged local issue, so
// it is not re-pushed on every sync. beads-5ahf.
func TestPushFieldsEqualIgnoresIdempotencyMarker(t *testing.T) {
	local := &types.Issue{
		Title:       "Sync me once",
		Description: "The canonical body.",
		Priority:    2,
		Status:      types.StatusOpen,
	}
	marker := GenerateIdempotencyMarker("bd-eq", "e@q.c", 7)
	cfg := &MappingConfig{}
	remote := &Issue{
		Title:       "Sync me once",
		Description: AppendIdempotencyMarker(BuildLinearDescription(local), marker),
		// Match the priority the local issue maps to, and leave State nil so
		// StateToBeadsStatus defaults to open (== local). This isolates the test
		// to the description-marker axis: the ONLY difference is the marker.
		Priority: PriorityToLinear(local.Priority, cfg),
	}
	if !PushFieldsEqual(local, remote, cfg) {
		t.Errorf("PushFieldsEqual = false for an unchanged issue whose remote desc carries the idempotency marker; want true (would re-push every sync)")
	}
}
