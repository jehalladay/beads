package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-erw5 (lav0 MARSHAL-variant, success-half): `bd human list --json` built
// its response with json.MarshalIndent + fmt.Println, bypassing
// outputJSON→wrapWithSchemaVersion. The payload is a slice, so in non-envelope
// mode wrapWithSchemaVersion is a pass-through (a top-level array can't carry a
// schema_version key) — but under BD_JSON_ENVELOPE=1 the response MUST be
// wrapped {schema_version, data:[...]}, matching the `ready --json` control.
// The old raw-marshal site ignored the env var and always emitted a bare list,
// breaking a .data consumer. The fix routes through outputJSON via
// emitHumanListJSON. RED before the fix (bare list under envelope mode).
func TestHumanListJSONEnvelopeMode(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	t.Setenv("BD_JSON_ENVELOPE", "1")

	issues := []*types.Issue{{ID: "bd-erw5-1", Title: "one"}}

	out := captureStdout(t, func() error {
		return emitHumanListJSON(issues)
	})

	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		t.Fatalf("expected JSON on stdout, got empty")
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		t.Fatalf("envelope mode must emit a JSON object {schema_version,data}, got non-object: %v\nstdout:\n%s", err, out)
	}
	if _, ok := m["schema_version"]; !ok {
		t.Fatalf("human list --json (envelope) missing schema_version (beads-erw5): %v", m)
	}
	if _, ok := m["data"]; !ok {
		t.Fatalf("human list --json (envelope) must wrap the list under 'data' (beads-erw5): %v", m)
	}
}

// TestHumanListJSONEmptyIsArrayNotNull is the beads-b2yd regression: an empty
// human-needed set left `issues` as a typed-nil `[]*types.Issue`, which still
// satisfies reflect.Slice in wrapWithSchemaVersion and so marshalled to `null`
// (breaking `.data`/iteration consumers). The fix normalizes nil→`[]` in
// emitHumanListJSON. RED before the fix: stdout is `null`.
func TestHumanListJSONEmptyIsArrayNotNull(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	var empty []*types.Issue // nil slice — the "no human beads" case

	out := captureStdout(t, func() error {
		return emitHumanListJSON(empty)
	})

	trimmed := strings.TrimSpace(out)
	if trimmed == "null" {
		t.Fatalf("empty human list --json emitted `null` instead of `[]` (beads-b2yd)")
	}
	if !strings.HasPrefix(trimmed, "[") {
		t.Fatalf("empty human list --json must be an empty JSON array `[]`, got: %q", trimmed)
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		t.Fatalf("empty output is not a decodable JSON array: %v\nstdout:\n%s", err, out)
	}
	if len(arr) != 0 {
		t.Fatalf("expected an empty array, got %d elements: %v", len(arr), arr)
	}
}

// TestHumanStatsJSONContract is the beads-vath regression: `bd human stats
// --json` ignored the flag — its RunE called printHumanStats unconditionally,
// emitting the plaintext "Human Beads Stats" table with rc=0. The fix routes
// the computed counts through outputJSON. This drives the same emit path
// (outputJSON(humanStats)) and asserts a parseable JSON object carrying the
// four count fields. RED before the fix: the RunE never reaches outputJSON.
func TestHumanStatsJSONContract(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	issues := []*types.Issue{
		{ID: "bd-1", Status: "open"},
		{ID: "bd-2", Status: "closed", CloseReason: "Responded"},
		{ID: "bd-3", Status: "closed", CloseReason: "Dismissed: stale"},
	}
	stats := computeHumanStats(issues)

	out := captureStdout(t, func() error {
		return outputJSON(stats)
	})

	trimmed := strings.TrimSpace(out)
	if strings.Contains(trimmed, "Human Beads Stats") {
		t.Fatalf("human stats --json leaked the plaintext table onto stdout (beads-vath):\n%s", out)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		t.Fatalf("human stats --json is not a decodable JSON object: %v\nstdout:\n%s", err, out)
	}
	// In non-envelope mode the stats object carries schema_version alongside the
	// counts; assert the count fields are present and correct.
	for field, want := range map[string]float64{"total": 3, "pending": 1, "responded": 1, "dismissed": 1} {
		got, ok := m[field].(float64)
		if !ok {
			t.Fatalf("human stats --json missing numeric field %q: %v", field, m)
		}
		if got != want {
			t.Errorf("human stats --json field %q = %v, want %v", field, got, want)
		}
	}
}

// TestHumanListJSONPlainModeIsList guards the non-envelope contract: the
// response stays a bare JSON array (a top-level slice can't carry a
// schema_version key), matching `ready --json` and preserving existing
// consumers. This pins the fix to a behavior-preserving route through
// outputJSON, not an accidental shape change.
func TestHumanListJSONPlainModeIsList(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	issues := []*types.Issue{{ID: "bd-erw5-1", Title: "one"}}

	out := captureStdout(t, func() error {
		return emitHumanListJSON(issues)
	})

	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "[") {
		t.Fatalf("non-envelope human list --json must be a bare JSON array, got: %q", trimmed)
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		t.Fatalf("non-envelope output is not a JSON array: %v\nstdout:\n%s", err, out)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 issue in the list, got %d: %v", len(arr), arr)
	}
}
