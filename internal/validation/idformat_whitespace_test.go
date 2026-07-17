package validation

import "testing"

// ValidateIDFormat must reject IDs containing whitespace. Previously it did no
// whitespace check, so a mis-quoted "--id ' bd-x1 '" passed validation and
// yielded a prefix carrying the space (" bd"), causing a spurious prefix
// mismatch against the clean db prefix or a whitespace-corrupted stored ID that
// won't round-trip on lookup (beads-qpf0).
func TestValidateIDFormat_RejectsWhitespace(t *testing.T) {
	bad := []string{
		" bd-x1",  // leading space
		"bd-x1 ",  // trailing space
		" bd-x1 ", // both
		"bd -x1",  // embedded space in prefix
		"bd- x1",  // embedded space in suffix
		"bd-x1\t", // trailing tab
		"bd\t-x1", // embedded tab
		"bd-x1\n", // trailing newline
	}
	for _, id := range bad {
		t.Run(id, func(t *testing.T) {
			if _, err := ValidateIDFormat(id); err == nil {
				t.Errorf("ValidateIDFormat(%q) = nil error, want a whitespace rejection", id)
			}
		})
	}
}

// Well-formed IDs (including hyphenated prefixes) still validate.
func TestValidateIDFormat_AcceptsCleanIDs(t *testing.T) {
	good := map[string]string{
		"bd-a3f8e9":      "bd",
		"bd-42":          "bd",
		"bd-a3f8e9.1":    "bd",
		"bead-me-up-3e9": "bead-me-up",
		"":               "", // empty is allowed (means "auto-generate")
	}
	for id, wantPrefix := range good {
		t.Run(id, func(t *testing.T) {
			got, err := ValidateIDFormat(id)
			if err != nil {
				t.Fatalf("ValidateIDFormat(%q) unexpected error: %v", id, err)
			}
			if got != wantPrefix {
				t.Errorf("ValidateIDFormat(%q) = %q, want %q", id, got, wantPrefix)
			}
		})
	}
}
