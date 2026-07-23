package main

import (
	"strings"
	"testing"
)

// beads-3sjks: federation.sovereignty is a fixed-domain enum (T1-T4, or empty
// for no restriction) but is NOT in enumConfigKeys because its domain is
// case-insensitive and empty-valid — an exact-match enum entry would wrongly
// reject lowercase "t1" and the empty value. Before this fix `bd config set
// federation.sovereignty <bad>` passed every set-time check (federation. is a
// recognized prefix; not bool/int/enum) and persisted the junk value, which
// GetSovereignty then silently defaulted to T1 (the MOST-OPEN tier) on read —
// a silent security downgrade of a typo'd restrictive intent. The fix rejects
// at set-time via config.IsValidSovereignty (the same validator config validate
// uses). This is a pure unit guard on the shared chokepoint
// validateConfigValueType, exercised by both `bd config set` and `set-many`.
func TestValidateConfigValueType_federationSovereignty_3sjks(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		// valid domain accepted (canonical uppercase)
		{"T1", "T1", false},
		{"T2", "T2", false},
		{"T3", "T3", false},
		{"T4", "T4", false},
		// empty = "no restriction", explicitly valid
		{"empty no-restriction", "", false},
		// case-insensitive accept (read-path normalizes with ToUpper; an
		// exact-match enum entry would have wrongly rejected these)
		{"lowercase t1", "t1", false},
		{"lowercase t4", "t4", false},
		{"whitespace-padded t2", " T2 ", false},
		// out-of-domain rejected (previously persisted then downgraded to T1)
		{"typo T33", "T33", true},
		{"typo T0", "T0", true},
		{"typo T5", "T5", true},
		{"non-tier word", "public", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfigValueType("federation.sovereignty", tc.value)
			if tc.wantErr && err == nil {
				t.Errorf("validateConfigValueType(federation.sovereignty, %q) = nil, want rejection (out-of-domain value silently downgrades to T1 on read)", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateConfigValueType(federation.sovereignty, %q) = %v, want nil", tc.value, err)
			}
			// A rejection must name a valid tier so the user can self-correct.
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "T1") {
				t.Errorf("rejection for %q should list the valid tiers; got %v", tc.value, err)
			}
		})
	}
}
