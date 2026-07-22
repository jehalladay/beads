//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-ixb4w: validation.on-create and validation.on-close are fixed-domain
// enum keys (none|warn|error, default "none") consumed by create.go/close.go
// (and the proxied create path) with a `!= "error" && != "warn"` check. Before
// this fix they were NOT in enumConfigKeys, so `bd config set
// validation.on-create warm` persisted the typo unchecked and the read-path
// treated the out-of-domain value as DISABLED — validation silently OFF while
// the user believed it was on (a false-success footgun, the write-path leg of
// the beads-m83zh enum class). This is the shared-chokepoint (validateConfigValueType,
// exercised by both `bd config set` and `bd config set-many`) unit guard.
func TestValidateConfigValueType_validationEnum_ixb4w(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		// valid domain accepted
		{"on-create none", "validation.on-create", "none", false},
		{"on-create warn", "validation.on-create", "warn", false},
		{"on-create error", "validation.on-create", "error", false},
		{"on-close none", "validation.on-close", "none", false},
		{"on-close warn", "validation.on-close", "warn", false},
		{"on-close error", "validation.on-close", "error", false},
		// out-of-domain typos rejected (previously silently disabled validation)
		{"on-create typo warm", "validation.on-create", "warm", true},
		{"on-create typo off", "validation.on-create", "off", true},
		{"on-create empty", "validation.on-create", "", true},
		{"on-close typo eror", "validation.on-close", "eror", true},
		{"on-close typo strict", "validation.on-close", "strict", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfigValueType(tc.key, tc.value)
			if tc.wantErr && err == nil {
				t.Errorf("validateConfigValueType(%q, %q) = nil, want enum-domain rejection (out-of-domain value silently disables validation)", tc.key, tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateConfigValueType(%q, %q) = %v, want nil", tc.key, tc.value, err)
			}
			// A rejection must name the valid set so the user can self-correct.
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "none") {
				t.Errorf("rejection for %q should list the valid values; got %v", tc.key, err)
			}
		})
	}
}
