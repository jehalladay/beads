//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDeleteSourceWispAuxCount_dtmj2 pins beads-dtmj2: bd purge/prune
// under-reported the wisp_labels / wisp_events counts for a directly-purged wisp.
//
// wisp_labels and wisp_events DO carry an ON DELETE CASCADE FK to wisps(id)
// (added by migrations/ignored/0004; the "ignored" dir is dolt_ignore data-
// exclusion, not unapplied — SHOW CREATE TABLE confirms the FK at runtime). So a
// directly-purged wisp (initialWispIDs, omitted from cascadeWispIDs) had its
// label/event rows FK-cascade-removed but counted 0 — the same class g7rof fixed
// for wisp_dependencies. The fix counts wisp_labels/wisp_events over
// allWispIDsDedup (initialWispIDs ∪ cascadeWispIDs, deduped).
//
// Embedded backend: it passes wisp IDs straight to DeleteIssuesInTx (the server
// DoltStore pre-partitions wisps out via deleteWispBatch).
//
// MUTATION-VERIFIED: reverting either count to cascadeWispIDs drops the asserted
// counts back below the actual removed-row totals.
func TestDeleteSourceWispAuxCount_dtmj2(t *testing.T) {
	te := newTestEnv(t, "dtmj2")
	ctx := t.Context()

	// Ephemeral wisp W — the directly-purged issue, carrying a label. Its label
	// row lives in wisp_labels (issue_id = W); creating/labeling it also emits
	// wisp_events rows.
	w := &types.Issue{
		ID: "dtmj2-wisp-1", Title: "wisp with aux", Status: types.StatusOpen,
		Priority: 3, IssueType: types.TypeTask, Ephemeral: true,
	}
	if err := te.store.CreateIssue(ctx, w, "tester"); err != nil {
		t.Fatalf("create W (wisp): %v", err)
	}
	if err := te.store.AddLabel(ctx, w.ID, "leaky", "tester"); err != nil {
		t.Fatalf("add label to W: %v", err)
	}

	// Ground truth: how many aux rows are keyed to W right before the purge.
	var wantLabels, wantEvents int
	te.queryScalar(t, ctx, `SELECT COUNT(*) FROM wisp_labels WHERE issue_id = ?`, []any{w.ID}, &wantLabels)
	te.queryScalar(t, ctx, `SELECT COUNT(*) FROM wisp_events WHERE issue_id = ?`, []any{w.ID}, &wantEvents)
	if wantLabels == 0 || wantEvents == 0 {
		t.Fatalf("precondition: expected >=1 label + >=1 event before purge, got %d/%d", wantLabels, wantEvents)
	}

	// Dry-run purge of the directly-named wisp (count-only).
	result, err := te.store.DeleteIssues(ctx, []string{w.ID}, false, true, true)
	if err != nil {
		t.Fatalf("delete wisp: %v", err)
	}
	if result.LabelsCount != wantLabels {
		t.Errorf("LabelsCount = %d, want %d — wisp_labels count missed the directly-purged wisp (beads-dtmj2)", result.LabelsCount, wantLabels)
	}
	if result.EventsCount != wantEvents {
		t.Errorf("EventsCount = %d, want %d — wisp_events count missed the directly-purged wisp (beads-dtmj2)", result.EventsCount, wantEvents)
	}
}
