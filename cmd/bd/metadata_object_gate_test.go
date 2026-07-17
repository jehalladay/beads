package main

import "testing"

// TestMetadataIsJSONObject covers the beads-ef2k input gate: --metadata must be
// a JSON object (or empty/null), because arrays/scalars pass json.Valid but then
// permanently lock the bead out of all metadata edit paths (merge/set/unset all
// unmarshal into map[string]json.RawMessage).
func TestMetadataIsJSONObject(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"object", `{"key":"value"}`, true},
		{"empty object", `{}`, true},
		{"nested object", `{"a":{"b":1},"c":[1,2]}`, true},
		{"empty string (== empty object)", ``, true},
		{"whitespace only", `   `, true},
		{"null (== empty object)", `null`, true},
		{"object with leading/trailing space", `  {"k":1}  `, true},
		{"array rejected", `[1,2,3]`, false},
		{"number scalar rejected", `42`, false},
		{"string scalar rejected", `"str"`, false},
		{"bool scalar rejected", `true`, false},
		{"empty array rejected", `[]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := metadataIsJSONObject(tc.in); got != tc.want {
				t.Fatalf("metadataIsJSONObject(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
