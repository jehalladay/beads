package main

import "testing"

// config set-many must type-validate bool/int keys the same as config set
// (beads-9v32). This exercises validateConfigValueType — the guard now wired
// into the set-many per-pair loop — against the values that were silently
// accepted before (and then read back as false/garbage by the consumer).
func TestConfigSetMany_TypeValidationParity(t *testing.T) {
	// Values that config set rejects and set-many must now also reject.
	reject := []struct{ key, value string }{
		{"backup.enabled", "maybe"},
		{"backup.enabled", "yes"}, // not a strconv bool
		{"backup.enabled", "on"},
		{"auto_compact_enabled", "notabool"},
		{"export.auto", "1.5"},
		{"output.title-length", "abc"},
		{"output.title-length", "12x"},
		{"schema_version", "v3"},
	}
	for _, tc := range reject {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			if err := validateConfigValueType(tc.key, tc.value); err == nil {
				t.Errorf("validateConfigValueType(%q,%q) = nil, want rejection (set-many parity)", tc.key, tc.value)
			}
		})
	}

	// Lifecycle-owned keys must be rejected in set-many too (beads-9v32),
	// matching config set — writing issue_prefix via config produces an
	// invisible key that bd create never reads.
	for _, k := range []string{"issue_prefix", "issue-prefix"} {
		t.Run("protected/"+k, func(t *testing.T) {
			if _, rejected := rejectProtectedConfigKey(k); !rejected {
				t.Errorf("rejectProtectedConfigKey(%q) not rejected — set-many would silently write an invisible key", k)
			}
		})
	}

	// Valid values must still pass.
	accept := []struct{ key, value string }{
		{"backup.enabled", "true"},
		{"backup.enabled", "false"},
		{"auto_compact_enabled", "1"}, // strconv-bool accepts 1/0
		{"output.title-length", "255"},
		{"output.title-length", "-1"}, // negative int is a valid int (title-length treats <=0 as hide)
		{"schema_version", "53"},
		{"jira.url", "https://x.test"}, // untyped key: no type check
	}
	for _, tc := range accept {
		t.Run("ok/"+tc.key+"="+tc.value, func(t *testing.T) {
			if err := validateConfigValueType(tc.key, tc.value); err != nil {
				t.Errorf("validateConfigValueType(%q,%q) = %v, want nil", tc.key, tc.value, err)
			}
		})
	}
}
