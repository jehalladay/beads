package gitlab

import "testing"

// beads-famu: PriorityToTracker's reverse lookup must be DETERMINISTIC on a
// non-injective PriorityMap (two labels mapping to the same beads priority).
// Before the fix it iterated the map directly, so Go's randomized map order
// made it return an arbitrary label that varied across runs — which can cause
// spurious "changed" detections on push. The fix iterates label keys sorted.
func TestPriorityToTrackerDeterministicOnNonInjectiveMap(t *testing.T) {
	t.Parallel()
	// Both "critical" and "urgent" map to beads priority 0 (non-injective).
	m := &gitlabFieldMapper{config: &MappingConfig{
		PriorityMap: map[string]int{
			"critical": 0,
			"urgent":   0,
			"high":     1,
			"medium":   2,
		},
	}}

	// Repeated calls must return the SAME label (sorted → "critical" wins over
	// "urgent"), not a randomly-varying one.
	first := m.PriorityToTracker(0)
	if first != "critical" {
		t.Fatalf("PriorityToTracker(0) = %v, want deterministic \"critical\" (sorted-first of the tied labels)", first)
	}
	for i := 0; i < 200; i++ {
		if got := m.PriorityToTracker(0); got != first {
			t.Fatalf("PriorityToTracker(0) non-deterministic: iteration %d got %v, first got %v", i, got, first)
		}
	}
}
