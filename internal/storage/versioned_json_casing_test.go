package storage

import (
	"encoding/json"
	"testing"
)

// beads-8slh: DiffEntry and Conflict lacked json tags, so marshaling them (via
// `bd diff --json` and `bd federation sync --json` → SyncResult.Conflicts)
// emitted PascalCase Go field names (IssueID/DiffType/OursValue/...) while the
// sibling HistoryEntry — and every types.Issue field — is snake_case. That
// inconsistency breaks a consumer doing resp["issue_id"] (KeyError). These
// tests marshal each struct and assert the snake_case keys are present and the
// PascalCase Go names are absent, failing before the tags were added.

func TestDiffEntryJSONSnakeCase_8slh(t *testing.T) {
	e := DiffEntry{IssueID: "bd-1", DiffType: "modified"}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal DiffEntry: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal DiffEntry json: %v\ngot: %s", err, b)
	}
	for _, want := range []string{"issue_id", "diff_type", "old_value", "new_value"} {
		if _, ok := m[want]; !ok {
			t.Errorf("DiffEntry --json missing snake_case key %q; got keys %v", want, keys(m))
		}
	}
	for _, bad := range []string{"IssueID", "DiffType", "OldValue", "NewValue"} {
		if _, ok := m[bad]; ok {
			t.Errorf("DiffEntry --json leaked PascalCase Go field name %q; got keys %v", bad, keys(m))
		}
	}
}

func TestConflictJSONSnakeCase_8slh(t *testing.T) {
	c := Conflict{IssueID: "bd-1", Field: "title", OursValue: "a", TheirsValue: "b"}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal Conflict: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal Conflict json: %v\ngot: %s", err, b)
	}
	for _, want := range []string{"issue_id", "field", "ours_value", "theirs_value"} {
		if _, ok := m[want]; !ok {
			t.Errorf("Conflict --json missing snake_case key %q; got keys %v", want, keys(m))
		}
	}
	for _, bad := range []string{"IssueID", "Field", "OursValue", "TheirsValue"} {
		if _, ok := m[bad]; ok {
			t.Errorf("Conflict --json leaked PascalCase Go field name %q; got keys %v", bad, keys(m))
		}
	}
}

func keys(m map[string]json.RawMessage) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
