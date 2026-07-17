package jira

import (
	"encoding/json"
	"testing"
)

// TestDescriptionToPlainText_PreservesInteriorBlankLines is the beads-hyg4
// regression: PlainTextToADF emits an empty paragraph node for each blank
// line, but DescriptionToPlainText dropped every empty paragraph, so an
// interior blank line (paragraph break) was lost on round-trip. The fix
// preserves INTERIOR empty paragraphs (between non-empty ones) while still
// trimming leading/trailing empties — so the existing "" behavior for a lone
// or edge empty paragraph is unchanged (no fidelity-semantics decision needed).
func TestDescriptionToPlainText_PreservesInteriorBlankLines(t *testing.T) {
	adfDoc := func(paras ...string) json.RawMessage {
		blocks := make([]map[string]interface{}, 0, len(paras))
		for _, p := range paras {
			var content []interface{}
			if p != "" {
				content = []interface{}{map[string]interface{}{"type": "text", "text": p}}
			} else {
				content = []interface{}{}
			}
			blocks = append(blocks, map[string]interface{}{"type": "paragraph", "content": content})
		}
		raw, _ := json.Marshal(map[string]interface{}{"type": "doc", "content": blocks})
		return raw
	}

	tests := []struct {
		name string
		in   json.RawMessage
		want string
	}{
		// The bug: one interior blank line must survive.
		{"interior blank", adfDoc("a", "", "b"), "a\n\nb"},
		// Multiple consecutive interior blanks survive.
		{"two interior blanks", adfDoc("a", "", "", "b"), "a\n\n\nb"},
		// Unchanged behavior (regression guards):
		{"two non-empty paras", adfDoc("First paragraph", "Second paragraph"), "First paragraph\nSecond paragraph"},
		{"lone empty paragraph", adfDoc(""), ""},
		{"trailing empty dropped", adfDoc("a", ""), "a"},
		{"leading empty dropped", adfDoc("", "a"), "a"},
		{"single paragraph", adfDoc("only"), "only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DescriptionToPlainText(tt.in); got != tt.want {
				t.Errorf("DescriptionToPlainText() = %q, want %q", got, tt.want)
			}
		})
	}
}
