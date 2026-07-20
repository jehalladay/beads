package main

import (
	"strings"
	"testing"
)

// TestValidateGateCheckType is the teeth for beads-68cgv: `bd gate check --type`
// must reject an unknown/typo/retired filter value up front rather than silently
// matching zero gates and printing "No open gates of type X found" + exit 0.
//
// The fail-late footgun: shouldCheckGate uses exact-match on the filter, so a
// typo like "ghpr" (vs "gh:pr") or the retired "bead" type falls through, checks
// NOTHING, and reads as "all clear" while real gh:pr/timer gates go unchecked.
// This mirrors the ds9tr validateGateCreate fail-early guard on the create side.
func TestValidateGateCheckType(t *testing.T) {
	tests := []struct {
		name       string
		typeFilter string
		wantErr    bool
	}{
		// valid: empty (= all), the "all"/"gh" aggregates, and each concrete type
		{"empty accepted", "", false},
		{"all accepted", "all", false},
		{"gh aggregate accepted", "gh", false},
		{"gh:run accepted", "gh:run", false},
		{"gh:pr accepted", "gh:pr", false},
		{"timer accepted", "timer", false},
		{"human accepted", "human", false},

		// invalid: retired type, typos, arbitrary junk
		{"retired bead rejected", "bead", true},
		{"typo ghpr rejected", "ghpr", true},
		{"typo ghrun rejected", "ghrun", true},
		{"arbitrary junk rejected", "banana", true},
		{"gh: bare rejected", "gh:", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGateCheckType(tt.typeFilter)
			if tt.wantErr && err == nil {
				t.Errorf("validateGateCheckType(%q) = nil, want error", tt.typeFilter)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateGateCheckType(%q) = %v, want nil", tt.typeFilter, err)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), "invalid gate type filter") {
				t.Errorf("validateGateCheckType(%q) error = %q, want it to mention 'invalid gate type filter'", tt.typeFilter, err.Error())
			}
		})
	}
}
