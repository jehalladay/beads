//go:build cgo

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// writeRawJSONL writes literal JSONL lines verbatim — unlike writeJSONLFile,
// which marshals a full types.Issue struct (and therefore can never OMIT a
// field). Preserve-on-absent (beads-w258p) is entirely about which fields the
// source line LITERALLY carries, so the teeth must control the raw bytes.
func writeRawJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write raw JSONL: %v", err)
	}
}

// showIssue reads an issue back via `bd show --json` and parses it into a
// types.Issue, so tests can assert the stored priority / scalar columns.
func showIssue(t *testing.T, bd, dir, id string) *types.Issue {
	t.Helper()
	raw := bdShowJSON(t, bd, dir, id)
	return parseIssueJSON(t, []byte(raw))
}

// TestEmbeddedImportPreserveOnAbsent_w258p proves the import upsert preserves
// fields that an incoming newer-updated_at JSONL line OMITS, at parity with
// labels (which already preserve-on-absent on the same upsert).
//
// Before beads-w258p, a partial newer-ts line SILENTLY:
//   - reset priority to 0/P0 (priority is a non-omitempty int, so an absent
//     priority decodes to Go-zero 0 = critical — a silent severity escalation);
//   - CLEARED absent scalar columns (description/notes/design/assignee) to "";
//   - while PRESERVING labels — an inconsistent absent-field policy.
//
// The fix threads per-line field presence (the peek map at import.go) into
// importIssuesCore, which carries the local value forward for any rewritten
// column the line omitted on an update-over-existing. An explicit "priority":0
// on the line still sets P0 (presence, not value, gates the restore), keeping
// the documented export|import round-trip byte-identical.
func TestEmbeddedImportPreserveOnAbsent_w258p(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "wp")

	// A far-future updated_at makes every import line strictly newer than the
	// just-created local row, so the stale guard never skips it and the upsert
	// path (the thing under test) actually runs.
	const newerTS = "2027-06-01T00:00:00Z"

	// ===== TEST 1: an absent priority on a newer-ts update KEEPS the local
	// priority — no silent P3 → P0 escalation. =====

	t.Run("absent_priority_preserves_local_not_escalated_to_P0", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "keep priority", "--type", "task", "--priority", "3")
		if issue.Priority != 3 {
			t.Fatalf("setup: expected created priority 3, got %d", issue.Priority)
		}
		jsonl := filepath.Join(t.TempDir(), "p.jsonl")
		// id + title + description + updated_at; NO priority.
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"keep priority","description":"kept","updated_at":%q}`, issue.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		got := showIssue(t, bd, dir, issue.ID)
		if got.Priority != 3 {
			t.Errorf("absent priority silently changed P3 → P%d (beads-w258p) — must preserve local", got.Priority)
		}
	})

	// ===== TEST 2: absent scalar columns (description/notes/design/assignee)
	// on a newer-ts update STAY at the local value, matching labels. =====

	t.Run("absent_scalars_preserve_local_matching_labels", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "keep scalars",
			"--type", "task",
			"-d", "DESC", "--notes", "NOTES", "--design", "DESIGN",
			"--assignee", "alice", "-l", "lbl:a", "-l", "lbl:b")
		jsonl := filepath.Join(t.TempDir(), "s.jsonl")
		// only id + title + updated_at — every scalar + labels omitted.
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"keep scalars","updated_at":%q}`, issue.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		got := showIssue(t, bd, dir, issue.ID)
		if got.Description != "DESC" {
			t.Errorf("absent description was cleared to %q — must preserve local (beads-w258p)", got.Description)
		}
		if got.Notes != "NOTES" {
			t.Errorf("absent notes was cleared to %q — must preserve local (beads-w258p)", got.Notes)
		}
		if got.Design != "DESIGN" {
			t.Errorf("absent design was cleared to %q — must preserve local (beads-w258p)", got.Design)
		}
		if got.Assignee != "alice" {
			t.Errorf("absent assignee was cleared to %q — must preserve local (beads-w258p)", got.Assignee)
		}
		// Regression guard: labels already preserved-on-absent; must stay so.
		if !hasLabel(got, "lbl:a") || !hasLabel(got, "lbl:b") {
			t.Errorf("labels not preserved on absent-field import: %v (regression)", got.Labels)
		}
	})

	// ===== TEST 3: an EXPLICIT "priority":0 on the line STILL sets P0 — the
	// round-trip case (export emits every field) must stay reachable. Presence,
	// not value, gates the restore. =====

	t.Run("explicit_priority_zero_still_sets_P0", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "explicit zero", "--type", "task", "--priority", "3")
		jsonl := filepath.Join(t.TempDir(), "z.jsonl")
		// priority present and explicitly 0.
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"explicit zero","priority":0,"updated_at":%q}`, issue.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		got := showIssue(t, bd, dir, issue.ID)
		if got.Priority != 0 {
			t.Errorf("explicit priority:0 did not set P0 (got P%d) — presence-gated restore must not swallow an explicit value", got.Priority)
		}
	})

	// ===== TEST 4: a PRESENT field with a new value on a newer-ts line is
	// still applied (preserve-on-absent must not turn into preserve-always). =====

	t.Run("present_fields_still_applied", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "apply present",
			"--type", "task", "--priority", "3", "-d", "OLD", "--assignee", "alice")
		jsonl := filepath.Join(t.TempDir(), "a.jsonl")
		// priority + description present with NEW values; assignee omitted.
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"apply present","priority":1,"description":"NEW","updated_at":%q}`, issue.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		got := showIssue(t, bd, dir, issue.ID)
		if got.Priority != 1 {
			t.Errorf("present priority=1 was not applied (got P%d) — preserve-on-absent must not block present fields", got.Priority)
		}
		if got.Description != "NEW" {
			t.Errorf("present description was not applied (got %q)", got.Description)
		}
		if got.Assignee != "alice" {
			t.Errorf("absent assignee not preserved (got %q) alongside applied present fields", got.Assignee)
		}
	})

	// ===== TEST 5: a genuinely-NEW issue (no local row) imports as-is — the
	// preserve pass must be a no-op for creates (absent priority defaults to 0
	// via SetDefaults, unchanged legacy behavior). =====

	t.Run("new_issue_unaffected_by_preserve_pass", func(t *testing.T) {
		jsonl := filepath.Join(t.TempDir(), "n.jsonl")
		newID := "wp-new-w258p"
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"brand new","description":"body","updated_at":%q}`, newID, newerTS))
		bdImport(t, bd, dir, jsonl)

		got := showIssue(t, bd, dir, newID)
		if got.Title != "brand new" || got.Description != "body" {
			t.Errorf("new issue did not import cleanly: title=%q desc=%q", got.Title, got.Description)
		}
	})
}
