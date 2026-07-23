//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestEmbeddedLabelBatchDedup_hzg2y is the direct-path teeth for beads-hzg2y:
// a repeated issue id in ONE `bd label add`/`bd label remove` batch must be
// processed exactly once. Without the uniqueStrings(issueIDs) dedup at the
// command entry (label.go, mirroring delete.go:86):
//
//   - `bd label add X X foo` double-counts: addLabelsHonoringNoChange pre-reads
//     GetLabels for BOTH occurrences of X before the single post-loop commit, so
//     the 2nd read still sees foo absent → TWO status:"added" JSON entries (and
//     newCount=2 in the commit message) even though AddLabelInTx writes once.
//   - `bd label remove X X foo` (X carries foo) double-prints: the present/missing
//     pre-check partitions [X,X] as both-present → processBatchLabelOperation
//     emits TWO status:"removed" entries though RemoveLabelInTx acts once.
//
// The dedup keeps first occurrence, so the reported result reflects the single
// genuine write. Genuinely-distinct ids in a batch are unaffected (negative
// controls). This is the DIRECT half; the proxied twin (which diverges the other
// way — a phantom "unchanged" on add and a spurious RC1 on remove) is covered by
// label_batch_dedup_proxied_hzg2y_test.go.
func TestEmbeddedLabelBatchDedup_hzg2y(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hz")

	// parseLabelResults extracts the flat [{status,issue_id,label}] array that
	// both the add and remove --json paths emit.
	type labelResult struct {
		Status  string `json:"status"`
		IssueID string `json:"issue_id"`
		Label   string `json:"label"`
	}
	parseLabelResults := func(t *testing.T, s string) []labelResult {
		t.Helper()
		s = strings.TrimSpace(s)
		start := strings.Index(s, "[")
		if start < 0 {
			t.Fatalf("no JSON array in output: %s", s)
		}
		var out []labelResult
		if err := json.Unmarshal([]byte(s[start:]), &out); err != nil {
			t.Fatalf("parse label results JSON: %v\nstdout: %s", err, s)
		}
		return out
	}

	t.Run("add_repeated_id_counts_once", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Add dup id", "--type", "task")
		out := bdLabelJSONOutput(t, bd, dir, "add", issue.ID, issue.ID, "dupid", "--json")
		res := parseLabelResults(t, out)
		if len(res) != 1 {
			t.Fatalf("expected exactly 1 result for a repeated id, got %d: %+v", len(res), res)
		}
		if res[0].Status != "added" || res[0].IssueID != issue.ID {
			t.Errorf("expected {added,%s}, got %+v", issue.ID, res[0])
		}
		// Storage is idempotent regardless — the label lands exactly once.
		labels := bdLabelListJSON(t, bd, dir, issue.ID)
		count := 0
		for _, l := range labels {
			if l == "dupid" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected exactly 1 'dupid' label, got %d in %v", count, labels)
		}
	})

	t.Run("add_distinct_ids_both_reported", func(t *testing.T) {
		// Negative control: two DIFFERENT ids in one batch must both be reported.
		i1 := bdCreate(t, bd, dir, "Add distinct 1", "--type", "task")
		i2 := bdCreate(t, bd, dir, "Add distinct 2", "--type", "task")
		out := bdLabelJSONOutput(t, bd, dir, "add", i1.ID, i2.ID, "distinctadd", "--json")
		res := parseLabelResults(t, out)
		if len(res) != 2 {
			t.Fatalf("expected 2 results for 2 distinct ids, got %d: %+v", len(res), res)
		}
		seen := map[string]bool{}
		for _, r := range res {
			if r.Status != "added" {
				t.Errorf("expected status added, got %+v", r)
			}
			seen[r.IssueID] = true
		}
		if !seen[i1.ID] || !seen[i2.ID] {
			t.Errorf("expected both %s and %s reported, got %+v", i1.ID, i2.ID, res)
		}
	})

	t.Run("remove_repeated_id_reports_once_rc0", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Remove dup id", "--type", "task", "--label", "rmdup")
		out := bdLabelJSONOutput(t, bd, dir, "remove", issue.ID, issue.ID, "rmdup", "--json")
		res := parseLabelResults(t, out)
		if len(res) != 1 {
			t.Fatalf("expected exactly 1 removed result for a repeated id, got %d: %+v", len(res), res)
		}
		if res[0].Status != "removed" || res[0].IssueID != issue.ID {
			t.Errorf("expected {removed,%s}, got %+v", issue.ID, res[0])
		}
		labels := bdLabelListJSON(t, bd, dir, issue.ID)
		for _, l := range labels {
			if l == "rmdup" {
				t.Errorf("label should have been removed, still present: %v", labels)
			}
		}
	})

	t.Run("remove_distinct_ids_both_reported", func(t *testing.T) {
		// Negative control: two DIFFERENT ids both carrying the label → both removed.
		i1 := bdCreate(t, bd, dir, "Remove distinct 1", "--type", "task", "--label", "rmdistinct")
		i2 := bdCreate(t, bd, dir, "Remove distinct 2", "--type", "task", "--label", "rmdistinct")
		out := bdLabelJSONOutput(t, bd, dir, "remove", i1.ID, i2.ID, "rmdistinct", "--json")
		res := parseLabelResults(t, out)
		if len(res) != 2 {
			t.Fatalf("expected 2 removed results for 2 distinct ids, got %d: %+v", len(res), res)
		}
	})
}
