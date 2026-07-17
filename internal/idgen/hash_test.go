package idgen

import (
	"testing"
	"time"
)

func TestEncodeBase36NonPositiveLength(t *testing.T) {
	// A negative length must not panic (make([]byte, 0, length) would crash
	// with "makeslice: cap out of range"); it and zero yield "" (beads-722j).
	for _, length := range []int{-1, -100, 0} {
		got := EncodeBase36([]byte{0xff, 0xff, 0xff}, length)
		if got != "" {
			t.Errorf("EncodeBase36(_, %d) = %q, want empty string", length, got)
		}
	}
}

func TestGenerateHashIDNonPositiveLengthDoesNotPanic(t *testing.T) {
	// Reachable from a corrupt min_hash_length config → ComputeAdaptiveLength
	// returns a negative length → GenerateHashID. Must not crash bd create.
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, length := range []int{-1, 0} {
		got := GenerateHashID("bd", "t", "d", "actor", ts, length, 0)
		// With an empty hash body the ID collapses to the prefix + "-".
		if got != "bd-" {
			t.Errorf("GenerateHashID length=%d = %q, want %q", length, got, "bd-")
		}
	}
}

func TestGenerateHashIDMatchesJiraVector(t *testing.T) {
	timestamp := time.Date(2024, 1, 2, 3, 4, 5, 6*1_000_000, time.UTC)
	prefix := "bd"
	title := "Fix login"
	description := "Details"
	creator := "jira-import"

	tests := map[int]string{
		3: "bd-vju",
		4: "bd-8d8e",
		5: "bd-bi3tk",
		6: "bd-8bi3tk",
		7: "bd-r5sr6bm",
		8: "bd-8r5sr6bm",
	}

	for length, expected := range tests {
		got := GenerateHashID(prefix, title, description, creator, timestamp, length, 0)
		if got != expected {
			t.Fatalf("length %d: got %s, want %s", length, got, expected)
		}
	}
}
