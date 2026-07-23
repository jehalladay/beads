//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-tckog: validation.metadata.mode is a fixed-domain enum key
// (none|warn|error, default "none") read by config.MetadataValidationMode()
// via a `switch mode { case "warn","error": return mode; default: return
// "none" }` and consumed by the storage layer (metadata_config.go,
// issueops/helpers.go, dolt/metadata_schema.go). Before this fix it was NOT in
// enumConfigKeys, so `bd config set validation.metadata.mode warm` persisted the
// typo unchecked and the read-path silently degraded the out-of-domain value to
// "none" — metadata schema validation silently OFF while the user believed it
// was on. Same false-success footgun and same write-path leg (the shared
// chokepoint validateConfigValueType, exercised by both `bd config set` and
// `bd config set-many`) as the sibling validation.on-create/on-close fix
// (beads-ixb4w). It was missed by the ixb4w sweep because the read lives under
// internal/config, not cmd/bd.
func TestValidateConfigValueType_metadataMode_tckog(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		// valid domain accepted
		{"metadata.mode none", "validation.metadata.mode", "none", false},
		{"metadata.mode warn", "validation.metadata.mode", "warn", false},
		{"metadata.mode error", "validation.metadata.mode", "error", false},
		// out-of-domain typos rejected (previously silently disabled validation)
		{"metadata.mode typo warm", "validation.metadata.mode", "warm", true},
		{"metadata.mode typo off", "validation.metadata.mode", "off", true},
		{"metadata.mode typo strict", "validation.metadata.mode", "strict", true},
		{"metadata.mode typo eror", "validation.metadata.mode", "eror", true},
		{"metadata.mode empty", "validation.metadata.mode", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfigValueType(tc.key, tc.value)
			if tc.wantErr && err == nil {
				t.Errorf("validateConfigValueType(%q, %q) = nil, want enum-domain rejection (out-of-domain value silently disables metadata validation)", tc.key, tc.value)
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
