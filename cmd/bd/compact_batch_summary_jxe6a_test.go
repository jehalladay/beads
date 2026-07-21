//go:build cgo

package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/compact"
)

// TestSummarizeCompactBatch_jxe6a is the teeth for beads-jxe6a.
//
// `bd compact --all --json` (runCompactAll) hardcoded "success": true in the
// batch envelope and returned rc0 even when CompactTier1Batch reported non-fatal
// per-issue failures (BatchResult.Err from GetIssue/CompactTier1). That produced
// a self-contradicting {success:true, failed:N} document at rc0, so a structured
// consumer could not detect a partially-failed compaction batch — the false-
// success + partial-exit class (siblings beads-uctf/z8z9/v0rp).
//
// runCompactAll now derives the envelope from summarizeCompactBatch, which sets
// Success = (Failed == 0) and carries the per-issue Failures. The cmd layer
// os.Exit(1)s when !Success. This unit-tests that helper (the live mapping), so
// reverting Success to an unconditional true is mutation-verified RED. A full
// merge-OK-with-one-issue-failing subprocess is not deterministically inducible
// (the batch runs over CheckEligibility-passing candidates; a per-issue failure
// needs an injected GetIssue/AI fault), which is why the outcome logic is a pure
// helper pinned here — the same seam-level teeth pattern as beads-00oy4.
func TestSummarizeCompactBatch_jxe6a(t *testing.T) {
	t.Parallel()

	t.Run("partial_batch_is_not_success_and_lists_failures", func(t *testing.T) {
		results := []compact.BatchResult{
			{IssueID: "bd-1", OriginalSize: 1000, CompactedSize: 400},
			{IssueID: "bd-2", OriginalSize: 500, Err: errors.New("not eligible for Tier 1 compaction")},
		}
		s := summarizeCompactBatch(results)

		if s.Success {
			t.Fatalf("jxe6a: a batch with a failed issue must NOT report Success=true")
		}
		if s.Succeeded != 1 || s.Failed != 1 {
			t.Errorf("counts: got succeeded=%d failed=%d, want 1/1", s.Succeeded, s.Failed)
		}
		if len(s.Failures) != 1 || s.Failures[0].IssueID != "bd-2" {
			t.Fatalf("failures must name the failed issue; got %+v", s.Failures)
		}
		if s.Failures[0].Error == "" {
			t.Errorf("jxe6a: the failure entry must carry the error string; got empty")
		}
		// Only the succeeded issue contributes to the byte totals.
		if s.OriginalSize != 1000 || s.SavedBytes != 600 {
			t.Errorf("byte totals: got original=%d saved=%d, want 1000/600", s.OriginalSize, s.SavedBytes)
		}

		// End-to-end JSON contract mirroring runCompactAll's --json map: success
		// is false and the failed issue is visible.
		output := map[string]interface{}{
			"success":   s.Success,
			"total":     len(results),
			"succeeded": s.Succeeded,
			"failed":    s.Failed,
		}
		if len(s.Failures) > 0 {
			output["failures"] = s.Failures
		}
		raw, err := json.Marshal(output)
		if err != nil {
			t.Fatalf("marshal compact batch output: %v", err)
		}
		var payload struct {
			Success  bool `json:"success"`
			Failed   int  `json:"failed"`
			Failures []struct {
				IssueID string `json:"issue_id"`
				Error   string `json:"error"`
			} `json:"failures"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("compact batch output must be valid JSON: %v\n%s", err, raw)
		}
		if payload.Success {
			t.Errorf("jxe6a: --json must carry success:false on a partial batch; got:\n%s", raw)
		}
		if payload.Failed != 1 || len(payload.Failures) != 1 {
			t.Errorf("jxe6a: --json must expose the failed issue; got:\n%s", raw)
		}
	})

	t.Run("all_succeeded_is_success_with_no_failures", func(t *testing.T) {
		results := []compact.BatchResult{
			{IssueID: "bd-1", OriginalSize: 1000, CompactedSize: 400},
			{IssueID: "bd-2", OriginalSize: 800, CompactedSize: 300},
		}
		s := summarizeCompactBatch(results)

		if !s.Success {
			t.Fatalf("a batch with no failed issue must report Success=true")
		}
		if s.Failed != 0 || len(s.Failures) != 0 {
			t.Errorf("a clean batch must have no failures; got failed=%d failures=%+v", s.Failed, s.Failures)
		}
		if s.Succeeded != 2 || s.OriginalSize != 1800 || s.SavedBytes != 1100 {
			t.Errorf("clean totals: got succeeded=%d original=%d saved=%d, want 2/1800/1100", s.Succeeded, s.OriginalSize, s.SavedBytes)
		}
	})

	t.Run("empty_batch_is_success", func(t *testing.T) {
		s := summarizeCompactBatch(nil)
		if !s.Success || s.Succeeded != 0 || s.Failed != 0 {
			t.Errorf("an empty batch is a vacuous success; got %+v", s)
		}
	})

	t.Run("wholly_failed_batch_is_not_success", func(t *testing.T) {
		results := []compact.BatchResult{
			{IssueID: "bd-1", Err: errors.New("fetch failed")},
			{IssueID: "bd-2", Err: errors.New("not eligible")},
		}
		s := summarizeCompactBatch(results)
		if s.Success {
			t.Fatalf("jxe6a: a wholly-failed batch must NOT report Success=true")
		}
		if s.Failed != 2 || s.Succeeded != 0 {
			t.Errorf("counts: got succeeded=%d failed=%d, want 0/2", s.Succeeded, s.Failed)
		}
	})
}
