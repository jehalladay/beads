package issueops

import "testing"

// TestUpsertCoversMutableFields is a regression test for beads-kalv: the issue
// UPSERT's ON DUPLICATE KEY UPDATE set (issueUpsertColumns) must rewrite every
// user-MUTABLE column the fresh INSERT writes. It previously omitted
// owner/pinned/mol_type/work_type, so `bd export → edit → re-import` over an
// EXISTING issue silently dropped edits to those fields (fresh-create had full
// fidelity; the UPSERT path did not). assignee was already covered.
//
// This asserts the mutable subset is present. Columns deliberately excluded
// from the UPSERT (immutable identity / creation-time / compaction-managed /
// wisp+event-specific) are NOT required here — only the fields a user can edit
// and re-sync via the JSONL round-trip.
func TestUpsertCoversMutableFields(t *testing.T) {
	// User-mutable, JSON-exported (types.go omitempty) columns that must survive
	// a re-import over an existing row.
	mutable := []string{
		"title", "description", "design", "acceptance_criteria", "notes",
		"status", "priority", "issue_type", "assignee", "estimated_minutes",
		"started_at", "closed_at", "external_ref", "close_reason", "metadata",
		// The beads-kalv gap: these are written on INSERT + exported but were
		// missing from the UPSERT set.
		"owner", "pinned", "mol_type", "work_type",
	}

	have := make(map[string]bool, len(issueUpsertColumns))
	for _, c := range issueUpsertColumns {
		have[c] = true
	}

	for _, col := range mutable {
		if !have[col] {
			t.Errorf("issueUpsertColumns is missing mutable column %q — edits to it are silently dropped on JSONL re-import over an existing issue (beads-kalv)", col)
		}
	}
}
