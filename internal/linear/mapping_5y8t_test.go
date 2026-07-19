package linear

import "testing"

// beads-5y8t: PriorityToLinear builds an inverse of config.PriorityMap. On a
// NON-INJECTIVE map (two linear priorities mapping to the same beads priority)
// the pre-fix code let Go's randomized map iteration pick an arbitrary linear
// value that varied across runs, which can make PushFieldsEqual flip-flop and
// trigger spurious re-pushes. The fix iterates linear keys in sorted order so
// the result is STABLE (smallest linear value wins a collision).
func TestPriorityToLinearDeterministicOnNonInjectiveMap(t *testing.T) {
	// Both linear "1" and "2" map to beads priority 0 (non-injective).
	config := &MappingConfig{
		PriorityMap: map[string]int{
			"1": 0,
			"2": 0,
			"3": 2,
		},
	}
	// Sorted linear keys → "1" before "2" → linear 1 wins the beads-0 collision.
	first := PriorityToLinear(0, config)
	if first != 1 {
		t.Fatalf("PriorityToLinear(0) = %d, want deterministic 1 (smallest linear key of the tied pair)", first)
	}
	for i := 0; i < 200; i++ {
		if got := PriorityToLinear(0, config); got != first {
			t.Fatalf("PriorityToLinear(0) non-deterministic: iteration %d got %d, first got %d", i, got, first)
		}
	}
}
