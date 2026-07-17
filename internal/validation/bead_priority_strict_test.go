package validation

import "testing"

// ParsePriority must reject strings with trailing (or embedded) non-numeric
// junk. Previously fmt.Sscanf("%d") stopped at the first non-digit and reported
// success, so "3xyz"/"3.9"/"2abc"/"1e3"/"0x2" were silently accepted as valid
// priorities (beads-itys).
func TestParsePriority_RejectsTrailingGarbage(t *testing.T) {
	garbage := []string{
		"3xyz",   // trailing letters
		"3.9",    // decimal — Sscanf grabbed the leading 3
		"2abc",   // trailing letters
		"1e3",    // scientific-looking — must NOT be 1 (nor 1000)
		"0x2",    // hex-looking — must NOT be 0 (nor 2)
		"3 4",    // two numbers
		"1,2",    // comma-separated
		"P3junk", // P-prefix with trailing junk
		"p2x",    // lowercase prefix with junk
	}
	for _, in := range garbage {
		t.Run(in, func(t *testing.T) {
			if got := ParsePriority(in); got != -1 {
				t.Errorf("ParsePriority(%q) = %d, want -1 (garbage must be rejected)", in, got)
			}
		})
	}
}

// ValidatePriority is the exported gate used by bd create/update/priority/
// batch/search/list; it must surface an error for the same garbage.
func TestValidatePriority_RejectsTrailingGarbage(t *testing.T) {
	for _, in := range []string{"3xyz", "3.9", "2abc", "1e3", "0x2", "P3junk"} {
		t.Run(in, func(t *testing.T) {
			if got, err := ValidatePriority(in); err == nil {
				t.Errorf("ValidatePriority(%q) = (%d, nil), want an error", in, got)
			}
		})
	}
}

// Guard the still-valid forms so the strict parse doesn't over-reject.
func TestParsePriority_StillAcceptsValidForms(t *testing.T) {
	valid := map[string]int{
		"0": 0, "4": 4, "P0": 0, "p3": 3, " 2 ": 2, " P1 ": 1,
	}
	for in, want := range valid {
		t.Run(in, func(t *testing.T) {
			if got := ParsePriority(in); got != want {
				t.Errorf("ParsePriority(%q) = %d, want %d", in, got, want)
			}
		})
	}
}
