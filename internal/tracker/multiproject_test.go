package tracker

import (
	"encoding/json"
	"testing"
)

func TestParseCommaSeparated(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"single", "abc", []string{"abc"}},
		{"two", "abc,def", []string{"abc", "def"}},
		{"whitespace", " abc , def , ghi ", []string{"abc", "def", "ghi"}},
		{"empty elements", "abc,,def,", []string{"abc", "def"}},
		{"empty string", "", []string{}},
		{"only commas", ",,,", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCommaSeparated(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseCommaSeparated(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDeduplicateStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"no dupes", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with dupes", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{"all same", []string{"x", "x", "x"}, []string{"x"}},
		{"empty", []string{}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeduplicateStrings(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("DeduplicateStrings(%v) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestResolveProjectIDs(t *testing.T) {
	tests := []struct {
		name      string
		cli       []string
		plural    string
		singular  string
		wantLen   int
		wantFirst string
	}{
		{"cli override", []string{"X", "Y"}, "A,B", "C", 2, "X"},
		{"plural config", nil, "A, B, C", "D", 3, "A"},
		{"singular fallback", nil, "", "D", 1, "D"},
		{"nothing", nil, "", "", 0, ""},
		{"cli dedup", []string{"A", "B", "A"}, "", "", 2, "A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveProjectIDs(tt.cli, tt.plural, tt.singular)
			if len(got) != tt.wantLen {
				t.Fatalf("got %v (len %d), want len %d", got, len(got), tt.wantLen)
			}
			if tt.wantLen > 0 && got[0] != tt.wantFirst {
				t.Errorf("first = %q, want %q", got[0], tt.wantFirst)
			}
		})
	}
}

// beads-jxel: the unconfigured path must return a non-nil empty slice, not
// nil. Callers embed the result directly in --json status output (linear
// "team_ids", jira "jira_projects", ado "projects"); a nil slice marshals to
// JSON null, breaking a consumer that expects an array. Guard the empty-slice
// contract at the shared root so all three trackers stay consistent.
func TestResolveProjectIDsEmptyIsNonNilArray_jxel(t *testing.T) {
	// Every path that yields zero IDs must produce a non-nil slice.
	cases := []struct {
		name     string
		cli      []string
		plural   string
		singular string
	}{
		{"nothing configured", nil, "", ""},
		{"empty cli override", []string{}, "", ""},
		{"plural is only commas", nil, ",,,", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveProjectIDs(tc.cli, tc.plural, tc.singular)
			if got == nil {
				t.Fatalf("ResolveProjectIDs returned nil; want non-nil empty slice (marshals to null otherwise)")
			}
			if len(got) != 0 {
				t.Fatalf("expected empty result, got %v", got)
			}
			// The bug manifests at serialization: nil → null, []string{} → [].
			raw, err := json.Marshal(map[string]interface{}{"team_ids": got})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(raw) != `{"team_ids":[]}` {
				t.Errorf("empty result must serialize as []; got %s", raw)
			}
		})
	}
}
