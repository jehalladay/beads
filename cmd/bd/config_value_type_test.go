//go:build cgo

package main

import "testing"

// beads-8fp: `bd config set` must reject a value whose type doesn't match a
// known-typed key (bool/int) at set-time, instead of silently storing a
// misconfiguration that only surfaces later when the parsing path runs.
func TestValidateConfigValueType(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		// bool keys: valid forms accepted (matches viper cast.ToBool / strconv.ParseBool)
		{"bool true", "export.auto", "true", false},
		{"bool false", "export.git-add", "false", false},
		{"bool 1", "no-push", "1", false},
		{"bool 0", "auto_compact_enabled", "0", false},
		// bool keys: type-invalid strings rejected
		{"bool notabool", "export.auto", "notabool", true},
		{"bool maybe", "export.git-add", "maybe", true},
		{"bool empty", "no-db", "", true},
		// int keys: valid + invalid
		{"int valid", "output.title-length", "40", false},
		{"int negative ok", "output.title-length", "-1", false},
		{"int not-a-number", "output.title-length", "not_a_number", true},
		{"int float rejected", "schema_version", "1.5", true},
		// untyped / unknown keys: stored as-is, never rejected here
		{"untyped custom key", "custom.whatever", "anything goes", false},
		{"untyped string key", "actor", "someuser", false},
		{"unknown key", "compact_batch_size", "not_a_number", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfigValueType(tc.key, tc.value)
			if tc.wantErr && err == nil {
				t.Errorf("validateConfigValueType(%q, %q) = nil, want error", tc.key, tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateConfigValueType(%q, %q) = %v, want nil", tc.key, tc.value, err)
			}
		})
	}
}
