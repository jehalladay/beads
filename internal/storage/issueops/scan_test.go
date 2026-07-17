package issueops

import (
	"testing"
	"time"
)

func TestParseTimeString(t *testing.T) {
	t.Parallel()

	rfc3339 := "2026-07-17T05:16:00Z"
	rfc3339Nano := "2026-07-17T05:16:00.123456789Z"
	mysql := "2026-07-17 05:16:00"

	tests := []struct {
		name  string
		input string
		want  time.Time
		zero  bool
	}{
		{name: "empty string is zero", input: "", zero: true},
		{name: "unparseable is zero", input: "not-a-time", zero: true},
		{
			name:  "RFC3339",
			input: rfc3339,
			want:  time.Date(2026, 7, 17, 5, 16, 0, 0, time.UTC),
		},
		{
			name:  "RFC3339Nano preserves fractional seconds",
			input: rfc3339Nano,
			want:  time.Date(2026, 7, 17, 5, 16, 0, 123456789, time.UTC),
		},
		{
			name:  "MySQL DATETIME",
			input: mysql,
			want:  time.Date(2026, 7, 17, 5, 16, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseTimeString(tt.input)
			if tt.zero {
				if !got.IsZero() {
					t.Fatalf("ParseTimeString(%q) = %v, want zero", tt.input, got)
				}
				return
			}
			if !got.Equal(tt.want) {
				t.Fatalf("ParseTimeString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseJSONStringArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty string is nil", input: "", want: nil},
		{name: "invalid JSON is nil", input: "{not json", want: nil},
		{name: "non-array JSON is nil", input: `{"a":1}`, want: nil},
		{name: "empty array", input: `[]`, want: []string{}},
		{name: "single element", input: `["a"]`, want: []string{"a"}},
		{name: "multiple elements", input: `["a","b","c"]`, want: []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseJSONStringArray(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseJSONStringArray(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ParseJSONStringArray(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}
