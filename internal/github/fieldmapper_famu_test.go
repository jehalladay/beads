package github

import "testing"

// beads-famu: github PriorityToTracker reverse lookup must be DETERMINISTIC on
// a non-injective PriorityMap (Go map iteration order is randomized). Sorted
// key iteration gives a stable label; default bijective map is unaffected.
func TestPriorityToTrackerDeterministicOnNonInjectiveMap(t *testing.T) {
	t.Parallel()
	m := &githubFieldMapper{config: &MappingConfig{
		PriorityMap: map[string]int{
			"critical": 0,
			"urgent":   0,
			"high":     1,
		},
	}}
	first := m.PriorityToTracker(0)
	if first != "critical" {
		t.Fatalf("PriorityToTracker(0) = %v, want deterministic \"critical\"", first)
	}
	for i := 0; i < 200; i++ {
		if got := m.PriorityToTracker(0); got != first {
			t.Fatalf("PriorityToTracker(0) non-deterministic: iter %d got %v, first %v", i, got, first)
		}
	}
}
