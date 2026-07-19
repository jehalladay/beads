package formula

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExtractVariablesEmptyIsNonNilArray_036h guards the json-ARRAY nil-slice
// contract: ExtractVariables on a formula with no {{variables}} must return a
// non-nil empty slice so `bd cook --json` emits "variables":[] not null.
// RED before the fix: `var vars []string` returned nil -> {"variables":null}.
func TestExtractVariablesEmptyIsNonNilArray_036h(t *testing.T) {
	f := &Formula{
		Description: "a formula with no variable references at all",
		Steps: []*Step{
			{Title: "step one", Description: "plain text"},
		},
	}
	got := ExtractVariables(f)
	if got == nil {
		t.Fatalf("ExtractVariables returned nil; want non-nil empty slice")
	}
	b, err := json.Marshal(map[string]any{"variables": got})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"variables":null`) {
		t.Fatalf("variables marshaled to null; want []: %s", b)
	}
	if !strings.Contains(string(b), `"variables":[]`) {
		t.Fatalf("expected variables:[] in %s", b)
	}
}

// Sanity: a formula WITH variables still returns them (no regression).
func TestExtractVariablesNonEmptyStillWorks_036h(t *testing.T) {
	f := &Formula{Description: "hello {{name}} and {{name}} again and {{other}}"}
	got := ExtractVariables(f)
	if len(got) != 2 {
		t.Fatalf("got %v (len %d), want 2 unique vars", got, len(got))
	}
}
