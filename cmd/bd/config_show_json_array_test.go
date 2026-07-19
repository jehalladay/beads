package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// beads-u86f3 (nil-slice array-contract): `bd config show --json` marshals the
// collected []configEntry via outputJSON. When --source matches nothing,
// filterBySource returns a nil slice, which serializes to the JSON literal
// `null` instead of `[]` — breaking a `bd config show --json --source nomatch
// | jq '.[]'` consumer, unlike the sibling ready/blocked/list/gate(tamf)/epic
// array contracts. The fix normalizes entries nil->[]configEntry{} at the RunE
// json branch. This test drives the real RunE with a source that matches
// nothing and asserts the emitted json is an array, never bare `null`.
//
// RED before the fix: the RunE emits `null` (wrapWithSchemaVersion passes the
// nil slice through). GREEN after: `[]`.
func TestConfigShowJSONArrayContract_u86f3(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	// A source that matches no entry drives filterBySource to a nil slice.
	if err := configShowCmd.Flags().Set("source", "no-such-source-xyzzy"); err != nil {
		t.Fatalf("set --source: %v", err)
	}
	defer func() { _ = configShowCmd.Flags().Set("source", "") }()

	out := captureConfigShowStdout(t, func() {
		if err := configShowCmd.RunE(configShowCmd, nil); err != nil {
			t.Fatalf("config show --json --source no-match: %v", err)
		}
	})

	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		t.Fatal("config show --json emitted no json on stdout")
	}

	// The payload (or its envelope .data) must be a JSON array, never `null`.
	// Unmarshal into a generic value and assert the array shape survives.
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		t.Fatalf("config show --json output is not valid json: %v\n%s", err, trimmed)
	}
	payload := v
	if m, ok := v.(map[string]any); ok {
		// BD_JSON_ENVELOPE mode wraps under "data"; otherwise a bare slice is
		// emitted at the top level (no schema_version key injected on slices).
		if d, has := m["data"]; has {
			payload = d
		}
	}
	if payload == nil {
		t.Fatalf("config show --json (no --source match): payload is bare null — must be an array [] (beads-u86f3)\n%s", trimmed)
	}
	if _, ok := payload.([]any); !ok {
		t.Fatalf("config show --json (no --source match): payload is %T, want a JSON array (beads-u86f3)\n%s", payload, trimmed)
	}
}

// captureConfigShowStdout runs fn with os.Stdout redirected to a pipe and
// returns what it wrote. Mirrors captureGraphStdout.
func captureConfigShowStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	_ = w.Close()
	os.Stdout = old
	<-done
	_ = r.Close()

	return buf.String()
}
