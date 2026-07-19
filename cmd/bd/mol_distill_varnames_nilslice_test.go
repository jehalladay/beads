package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGetVarNamesEmptyIsNonNilArray_036h guards the json-ARRAY nil-slice
// contract for `bd mol distill --json`: getVarNames on an empty replacement
// map must return a non-nil empty slice so DistillResult.Variables (no
// omitempty, emitted via outputJSON) marshals to [] not null.
// RED before the fix: `var names []string` returned nil -> {"variables":null}.
func TestGetVarNamesEmptyIsNonNilArray_036h(t *testing.T) {
	got := getVarNames(map[string]string{})
	if got == nil {
		t.Fatalf("getVarNames returned nil; want non-nil empty slice")
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

// Sanity: a populated replacement map still yields its names (no regression).
func TestGetVarNamesNonEmptyStillWorks_036h(t *testing.T) {
	got := getVarNames(map[string]string{"a": "x", "b": "y"})
	if len(got) != 2 {
		t.Fatalf("got %v (len %d), want 2", got, len(got))
	}
}
