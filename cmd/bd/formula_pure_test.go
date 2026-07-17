package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/formula"
)

// beads-6kd3: hermetic tests for the pure helpers in formula.go (verified 0% +
// no test references). No I/O — string/tree/map transforms.

func TestCapitalizeWord(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"hello":  "Hello",
		"Hello":  "Hello",
		"a":      "A",
		"123":    "123",    // non-letter first rune unchanged
		"éclair": "Éclair", // unicode first rune
	}
	for in, want := range cases {
		if got := capitalizeWord(in); got != want {
			t.Errorf("capitalizeWord(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetTypeIcon(t *testing.T) {
	cases := map[string]string{
		"workflow":  "📋",
		"expansion": "📐",
		"aspect":    "🎯",
		"convoy":    "🚐",
		"unknown":   "📜", // default
		"":          "📜",
	}
	for in, want := range cases {
		if got := getTypeIcon(in); got != want {
			t.Errorf("getTypeIcon(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCountSteps(t *testing.T) {
	if got := countSteps(nil); got != 0 {
		t.Errorf("countSteps(nil) = %d, want 0", got)
	}
	// Flat: 3 steps.
	flat := []*formula.Step{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	if got := countSteps(flat); got != 3 {
		t.Errorf("countSteps(flat) = %d, want 3", got)
	}
	// Nested: 1 root + 2 children + 1 grandchild = 4.
	nested := []*formula.Step{
		{ID: "root", Children: []*formula.Step{
			{ID: "c1", Children: []*formula.Step{{ID: "gc"}}},
			{ID: "c2"},
		}},
	}
	if got := countSteps(nested); got != 4 {
		t.Errorf("countSteps(nested) = %d, want 4", got)
	}
}

func TestConvertToMultiLineStrings(t *testing.T) {
	t.Run("description with escaped newlines becomes triple-quoted", func(t *testing.T) {
		in := `description = "line1\nline2"`
		out := convertToMultiLineStrings(in)
		if !strings.Contains(out, `"""`) {
			t.Errorf("expected triple-quoted block, got %q", out)
		}
		if strings.Contains(out, `\n`) {
			t.Errorf("escaped newlines should be unescaped, got %q", out)
		}
		if !strings.Contains(out, "line1\nline2") {
			t.Errorf("expected real newline between lines, got %q", out)
		}
	})

	t.Run("non-description key with \\n is left unchanged", func(t *testing.T) {
		in := `title = "a\nb"`
		out := convertToMultiLineStrings(in)
		if out != in {
			t.Errorf("non-description line should be unchanged, got %q", out)
		}
	})

	t.Run("line without escaped newline is unchanged", func(t *testing.T) {
		in := `description = "single line"`
		if out := convertToMultiLineStrings(in); out != in {
			t.Errorf("plain line changed: %q", out)
		}
	})
}

func TestFixIntegerFields(t *testing.T) {
	m := map[string]interface{}{
		"version":  float64(3),   // known int field, whole → int64
		"priority": float64(2),   // known int field
		"ratio":    float64(1.5), // not a known int field → unchanged
		"count":    float64(10.0),
		"name":     "keep",
		"nested": map[string]interface{}{
			"max": float64(5), // recurse into maps
		},
		"list": []interface{}{
			map[string]interface{}{"version": float64(7)}, // recurse into slice-of-maps
		},
	}
	fixIntegerFields(m)

	if v, ok := m["version"].(int64); !ok || v != 3 {
		t.Errorf("version not converted to int64: %T %v", m["version"], m["version"])
	}
	if _, ok := m["priority"].(int64); !ok {
		t.Errorf("priority should be int64, got %T", m["priority"])
	}
	if _, ok := m["ratio"].(float64); !ok {
		t.Errorf("non-int-field ratio should stay float64, got %T", m["ratio"])
	}
	if m["name"] != "keep" {
		t.Errorf("string field mangled: %v", m["name"])
	}
	if v, ok := m["nested"].(map[string]interface{})["max"].(int64); !ok || v != 5 {
		t.Errorf("nested max not converted: %T", m["nested"].(map[string]interface{})["max"])
	}
	inner := m["list"].([]interface{})[0].(map[string]interface{})
	if _, ok := inner["version"].(int64); !ok {
		t.Errorf("slice-nested version not converted, got %T", inner["version"])
	}
}
