package main

import (
	"reflect"
	"testing"
)

// parseLabels must split on comma/newline ONLY, preserving spaces WITHIN a
// label. Multi-word labels are supported (bd label add "in progress"), so
// space-splitting them was a bug (beads-ehw7).
func TestParseLabels_PreservesMultiWordLabels(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"multi-word comma-separated", "in progress, needs review", []string{"in progress", "needs review"}},
		{"single word", "backend", []string{"backend"}},
		{"single-word list", "backend, urgent", []string{"backend", "urgent"}},
		{"newline-separated multi-word", "in progress\nneeds review", []string{"in progress", "needs review"}},
		{"trims surrounding space", "  in progress ,  needs review  ", []string{"in progress", "needs review"}},
		{"drops empty entries", "backend,, urgent,", []string{"backend", "urgent"}},
		{"empty content", "", nil},
		{"scoped label with space", "area: back end, prio: high", []string{"area: back end", "prio: high"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLabels(tt.content)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseLabels(%q) = %#v, want %#v", tt.content, got, tt.want)
			}
		})
	}
}

// parseDependencies keeps space-splitting — issue IDs never contain spaces, and
// authors may separate them with spaces, commas, or newlines interchangeably.
func TestParseDependencies_SplitsOnSpaceCommaNewline(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"comma-separated", "bd-10, bd-20", []string{"bd-10", "bd-20"}},
		{"space-separated", "bd-10 bd-20", []string{"bd-10", "bd-20"}},
		{"newline-separated", "bd-10\nbd-20", []string{"bd-10", "bd-20"}},
		{"mixed separators", "bd-10, bd-20 bd-30", []string{"bd-10", "bd-20", "bd-30"}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDependencies(tt.content)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseDependencies(%q) = %#v, want %#v", tt.content, got, tt.want)
			}
		})
	}
}
