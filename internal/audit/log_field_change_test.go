package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupBeadsDir creates a temp .beads dir accepted by beads.FindBeadsDir and
// points BEADS_DIR at it, returning the audit log path. Mirrors the harness in
// TestAppend_CreatesFileAndWritesJSONL.
func setupBeadsDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	beadsDir := filepath.Join(tmp, ".beads")
	if err := os.MkdirAll(beadsDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	return filepath.Join(beadsDir, FileName)
}

// readEntries reads and parses every JSONL entry in the audit log at path.
func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("open log: %v", err)
	}
	defer func() { _ = f.Close() }()

	var entries []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal entry %q: %v", sc.Text(), err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return entries
}

func TestLogFieldChange_NoOpWhenUnchanged(t *testing.T) {
	path := setupBeadsDir(t)

	// Equal old/new must write nothing at all — not even create the file.
	LogFieldChange("bd-1", "status", "open", "open", "alice", "")

	if entries := readEntries(t, path); len(entries) != 0 {
		t.Fatalf("expected no audit entries for a no-op change, got %d", len(entries))
	}
}

func TestLogFieldChange_WritesEntryWithExtra(t *testing.T) {
	path := setupBeadsDir(t)

	LogFieldChange("bd-42", "status", "open", "closed", "bob", "")

	entries := readEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Kind != "field_change" {
		t.Errorf("Kind = %q, want %q", e.Kind, "field_change")
	}
	if e.IssueID != "bd-42" {
		t.Errorf("IssueID = %q, want %q", e.IssueID, "bd-42")
	}
	if e.Actor != "bob" {
		t.Errorf("Actor = %q, want %q", e.Actor, "bob")
	}
	if e.ID == "" {
		t.Error("entry ID should be populated")
	}
	for k, want := range map[string]string{"field": "status", "old_value": "open", "new_value": "closed"} {
		if got, _ := e.Extra[k].(string); got != want {
			t.Errorf("Extra[%q] = %v, want %q", k, e.Extra[k], want)
		}
	}
	// No reason supplied → the reason key must be absent.
	if _, ok := e.Extra["reason"]; ok {
		t.Errorf("Extra should not contain reason when none supplied, got %v", e.Extra["reason"])
	}
}

func TestAppend_ValidatesEntry(t *testing.T) {
	if _, err := Append(nil); err == nil {
		t.Error("Append(nil) should error")
	}
	if _, err := Append(&Entry{}); err == nil {
		t.Error("Append with empty Kind should error")
	}
}

func TestAppend_PreservesSuppliedIDAndTimestamp(t *testing.T) {
	setupBeadsDir(t)

	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("x", 3600))
	id, err := Append(&Entry{ID: "int-preset", Kind: "tool_call", ToolName: "bd", CreatedAt: when})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if id != "int-preset" {
		t.Errorf("Append returned id %q, want preset %q", id, "int-preset")
	}

	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	entries := readEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].CreatedAt.Equal(when) {
		t.Errorf("CreatedAt = %v, want %v (equal instant, normalized to UTC)", entries[0].CreatedAt, when)
	}
}

func TestPath_ErrorsWithoutBeadsDir(t *testing.T) {
	// Point BEADS_DIR at a non-existent location and chdir to an empty temp dir
	// so FindBeadsDir cannot discover a project — Path must error.
	tmp := t.TempDir()
	t.Setenv("BEADS_DIR", filepath.Join(tmp, "does-not-exist"))
	t.Chdir(tmp)
	if _, err := Path(); err == nil {
		t.Error("Path() should error when no .beads directory can be found")
	}
}

func TestLogFieldChange_IncludesReasonWhenNonEmpty(t *testing.T) {
	path := setupBeadsDir(t)

	LogFieldChange("bd-7", "assignee", "alice", "carol", "mayor", "reassigned during triage")

	entries := readEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	reason, _ := entries[0].Extra["reason"].(string)
	if reason != "reassigned during triage" {
		t.Errorf("Extra[reason] = %q, want %q", reason, "reassigned during triage")
	}
	if got, _ := entries[0].Extra["field"].(string); got != "assignee" {
		t.Errorf("Extra[field] = %q, want %q", got, "assignee")
	}
}
