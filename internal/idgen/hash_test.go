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

// TestGenerateHashIDLongLengthHasEntropy is the beads-ioci guard: for lengths
// > 8 (reachable via an unclamped max_hash_length config), the hash body must
// carry entropy proportional to the length, not the flat 24-bit `default:
// numBytes=3` that zero-pads the ID (making a length-10 id like "bd-000009ni53"
// — only ~5 significant chars, defeating the adaptive-length collision math).
func TestGenerateHashIDLongLengthHasEntropy(t *testing.T) {
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, length := range []int{9, 10, 12} {
		// The leading char of the hash body must VARY across inputs. With only
		// 24 bits of entropy padded to `length` chars, the top chars are stuck
		// at '0' for essentially all inputs; real entropy varies them.
		leadingChars := map[byte]bool{}
		for nonce := 0; nonce < 500; nonce++ {
			id := GenerateHashID("bd", "title", "desc", "creator", ts, length, nonce)
			body := id[len("bd-"):]
			if len(body) != length {
				t.Fatalf("length=%d: body %q len=%d, want %d", length, body, len(body), length)
			}
			leadingChars[body[0]] = true
		}
		// With genuine entropy the leading char takes many distinct values across
		// 500 inputs. The buggy 24-bit-padded path pins it to '0' (1 value).
		if len(leadingChars) < 2 {
			t.Errorf("length=%d: leading hash char took %d distinct values across 500 inputs "+
				"(want many) — the id is zero-padded low-entropy (beads-ioci)", length, len(leadingChars))
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
