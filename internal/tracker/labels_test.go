package tracker

import (
	"reflect"
	"testing"
)

func TestParseLabelPrefix(t *testing.T) {
	tests := []struct {
		label      string
		wantPrefix string
		wantValue  string
	}{
		{"priority::high", "priority", "high"},
		{"type::bug", "type", "bug"},
		{"status::in_progress", "status", "in_progress"},
		{"bug", "", "bug"},
		{"", "", ""},
		// Only the first "::" separates; the remainder is the value.
		{"a::b::c", "a", "b::c"},
	}
	for _, tt := range tests {
		prefix, value := ParseLabelPrefix(tt.label)
		if prefix != tt.wantPrefix {
			t.Errorf("ParseLabelPrefix(%q) prefix = %q, want %q", tt.label, prefix, tt.wantPrefix)
		}
		if value != tt.wantValue {
			t.Errorf("ParseLabelPrefix(%q) value = %q, want %q", tt.label, value, tt.wantValue)
		}
	}
}

func TestPriorityFromLabels(t *testing.T) {
	priorityMap := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	tests := []struct {
		labels []string
		want   int
	}{
		{[]string{"priority::high"}, 1},
		{[]string{"priority::critical", "type::bug"}, 0},
		{[]string{"priority::HIGH"}, 1}, // case-insensitive value lookup
		{[]string{"type::bug"}, 2},      // no priority label -> default
		{[]string{"priority::unknown"}, 2},
		{nil, 2},
	}
	for _, tt := range tests {
		if got := PriorityFromLabels(tt.labels, priorityMap); got != tt.want {
			t.Errorf("PriorityFromLabels(%v) = %d, want %d", tt.labels, got, tt.want)
		}
	}
}

func TestTypeFromLabels(t *testing.T) {
	typeMap := map[string]string{"bug": "bug", "feature": "feature", "epic": "epic"}
	tests := []struct {
		labels []string
		want   string
	}{
		{[]string{"type::bug"}, "bug"},
		{[]string{"feature"}, "feature"}, // bare label
		{[]string{"type::FEATURE"}, "feature"},
		{[]string{"priority::high"}, "task"}, // no type label -> default
		{nil, "task"},
	}
	for _, tt := range tests {
		if got := TypeFromLabels(tt.labels, typeMap); got != tt.want {
			t.Errorf("TypeFromLabels(%v) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}

func TestFilterScopedLabels(t *testing.T) {
	labels := []string{"priority::high", "status::blocked", "type::bug", "backend", "urgent"}
	got := FilterScopedLabels(labels)
	want := []string{"backend", "urgent"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterScopedLabels(%v) = %v, want %v", labels, got, want)
	}
	// No scoped labels -> everything preserved.
	if got := FilterScopedLabels([]string{"a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("FilterScopedLabels preserved wrong set: %v", got)
	}
	// All scoped -> nil.
	if got := FilterScopedLabels([]string{"priority::low"}); got != nil {
		t.Errorf("FilterScopedLabels(all scoped) = %v, want nil", got)
	}
}
