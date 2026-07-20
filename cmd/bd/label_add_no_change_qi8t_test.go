//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLabelAddNoChange_qi8t is the teeth for beads-qi8t: `bd label add <id>
// <label>` on a label the issue ALREADY carries used to print "✓ Added label"
// (JSON status:added) with rc=0 — a false success (AddLabel is idempotent, so
// storage no-ops, but the CLI claimed a new add). The fix reports the honest
// distinction: genuinely-new → "added", already-present → "unchanged". This is
// the add-half of the false-success class; the remove-half (beads-yaux) already
// landed with the symmetric GetLabels pre-check.
func TestLabelAddNoChange_qi8t(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	issue := bdCreate(t, bd, dir, "qi8t label add no-change", "--type", "task")

	// First add → genuinely new → "added".
	out1 := bdLabel(t, bd, dir, "add", issue.ID, "urgent")
	if !strings.Contains(out1, "Added label 'urgent'") {
		t.Errorf("first add: want 'Added label urgent', got:\n%q", out1)
	}

	// Second add of the SAME label → no change → must NOT claim "Added".
	out2 := bdLabel(t, bd, dir, "add", issue.ID, "urgent")
	if strings.Contains(out2, "Added label 'urgent'") {
		t.Errorf("re-add of present label falsely reported 'Added' (beads-qi8t):\n%q", out2)
	}
	if !strings.Contains(out2, "no change") {
		t.Errorf("re-add of present label should report 'no change', got:\n%q", out2)
	}

	// The label is still present exactly once (idempotent storage preserved).
	labels := bdLabelListJSON(t, bd, dir, issue.ID)
	count := 0
	for _, l := range labels {
		if l == "urgent" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("label 'urgent' should be present exactly once, got %d (labels=%v)", count, labels)
	}

	// --json: re-add reports status:unchanged, not status:added.
	jsonOut := bdLabelJSONOutput(t, bd, dir, "add", issue.ID, "urgent", "--json")
	var results []map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOut)), &results); err != nil {
		t.Fatalf("parse --json output: %v\nraw:\n%s", err, jsonOut)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result object, got %d: %v", len(results), results)
	}
	if got := results[0]["status"]; got != "unchanged" {
		t.Errorf("re-add --json status: want 'unchanged', got %v", got)
	}

	// Mixed batch: a fresh label + the already-present one on the same issue →
	// the new one is "added", the present one "unchanged", both reported.
	// NB: when --label is used, ALL positionals are issue IDs (collectLabelArgs),
	// so both labels go through --label and issue.ID is the sole positional.
	mixed := bdLabelJSONOutput(t, bd, dir, "add", issue.ID, "--label", "urgent", "--label", "fresh", "--json")
	var mixedResults []map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(mixed)), &mixedResults); err != nil {
		t.Fatalf("parse mixed --json: %v\nraw:\n%s", err, mixed)
	}
	byLabel := map[string]string{}
	for _, r := range mixedResults {
		byLabel[r["label"].(string)] = r["status"].(string)
	}
	if byLabel["urgent"] != "unchanged" {
		t.Errorf("mixed batch: 'urgent' (present) want 'unchanged', got %q", byLabel["urgent"])
	}
	if byLabel["fresh"] != "added" {
		t.Errorf("mixed batch: 'fresh' (new) want 'added', got %q", byLabel["fresh"])
	}
}
