package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// TestRenderDiffJSONEmptyIsArray_guib is the beads-guib regression: when a
// diff has no changes (e.g. `bd diff main main --json`), renderDiff's len==0
// branch passed a nil []*storage.DiffEntry to outputJSON, which json.Marshals
// to the literal `null` rather than the `[]` a --json array consumer expects.
// renderDiff is shared by the direct and proxied-server paths, so this one fix
// (and this one test) covers both. It must emit `[]` on the empty result.
func TestRenderDiffJSONEmptyIsArray_guib(t *testing.T) {
	prev := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prev }()

	// nil entries is exactly what store.Diff returns for a no-change diff and
	// is the specific value that used to marshal to `null` (diff.go:63).
	var entries []*storage.DiffEntry

	out := captureStdout(t, func() error {
		return renderDiff(entries, "main", "main")
	})

	trimmed := strings.TrimSpace(out)
	if trimmed == "null" {
		t.Fatalf("renderDiff emitted bare `null` for an empty diff; a --json consumer expects `[]`\ngot: %q", out)
	}

	// Must be a valid JSON array (specifically the empty array), not null/object.
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		t.Fatalf("empty-diff --json output is not a JSON array: %v\ngot: %q", err, out)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty JSON array for a no-change diff, got %d elements: %q", len(arr), out)
	}
}
