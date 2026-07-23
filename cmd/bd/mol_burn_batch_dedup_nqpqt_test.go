//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMolBurnBatchDedup_nqpqt pins the beads-nqpqt fix: a repeated molecule id in
// ONE `bd mol burn X X` batch must be processed and reported exactly once. This is
// the in-batch-dup class (delete / hzg2y label / cncgt reopen / 4k0d8 defer /
// vam5w todo done) for `bd mol burn`. Without the uniqueStrings(args) dedup at the
// command entry, a PERSISTENT molecule id passed twice is categorized twice in
// burnMultipleMolecules' first pass:
//
//   - iter-1 burns the molecule via deleteBatch (real delete);
//   - iter-2's loadTemplateSubgraph fails on the now-deleted id → FailedCount++ →
//     the --json envelope reports "failed_count":1 and the command exits non-zero,
//     even though the ONE molecule the user named was fully burned.
//
// The dedup keeps first occurrence; genuinely-distinct ids are unaffected.
//
// Mutation check: remove `args = uniqueStrings(args)` in mol_burn.go's runMolBurn
// and repeated_id_burns_once goes RED (failed_count becomes 1 for one molecule).
func TestMolBurnBatchDedup_nqpqt(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "mb")

	// parseBatchBurn extracts the BatchBurnResult envelope `bd mol burn a b --json`
	// emits (multi-molecule path). failed_count>0 signals a (false) partial failure.
	type batchBurn struct {
		Results      []map[string]interface{} `json:"results"`
		TotalDeleted int                      `json:"total_deleted"`
		FailedCount  int                      `json:"failed_count"`
	}

	// A repeated persistent-molecule id in one batch must burn once with NO false
	// failure. Before the fix the second (deduped-away) occurrence tripped
	// loadTemplateSubgraph on the already-deleted id → failed_count:1 + exit 1.
	t.Run("repeated_id_burns_once", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "burn dup id", "--type", "task")
		cmd := exec.Command(bd, "mol", "burn", issue.ID, issue.ID, "--force", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		// After dedup the batch collapses to a single molecule → exit 0 and no
		// failed_count. A non-zero exit here is the bug (false partial failure).
		if err != nil {
			t.Fatalf("bd mol burn %s %s --force exited non-zero (false partial failure): %v\n%s", issue.ID, issue.ID, err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON object in mol burn output: %s", s)
		}
		// The deduped single-molecule path emits a BurnResult (deleted_count:1),
		// not the BatchBurnResult envelope — confirm no failure is reported either way.
		var env batchBurn
		if uerr := json.Unmarshal([]byte(s[start:]), &env); uerr == nil {
			if env.FailedCount != 0 {
				t.Fatalf("expected failed_count 0 for a single molecule burned once, got %d: %s", env.FailedCount, s)
			}
		}
		if strings.Contains(s, "\"failed_count\": 1") || strings.Contains(s, "\"failed_count\":1") {
			t.Fatalf("repeated id produced a false failed_count:1 for one molecule: %s", s)
		}
	})

	// Negative control: two DISTINCT persistent-molecule ids in one batch must both
	// burn with no false failure.
	t.Run("distinct_ids_both_burned", func(t *testing.T) {
		i1 := bdCreate(t, bd, dir, "burn distinct 1", "--type", "task")
		i2 := bdCreate(t, bd, dir, "burn distinct 2", "--type", "task")
		cmd := exec.Command(bd, "mol", "burn", i1.ID, i2.ID, "--force", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd mol burn %s %s --force failed: %v\n%s", i1.ID, i2.ID, err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON object in mol burn output: %s", s)
		}
		var env batchBurn
		if uerr := json.Unmarshal([]byte(s[start:]), &env); uerr != nil {
			t.Fatalf("parse mol burn JSON: %v\nstdout: %s", uerr, s)
		}
		if env.FailedCount != 0 {
			t.Fatalf("expected failed_count 0 for two distinct molecules, got %d: %s", env.FailedCount, s)
		}
		if env.TotalDeleted != 2 {
			t.Fatalf("expected total_deleted 2 for two distinct molecules, got %d: %s", env.TotalDeleted, s)
		}
	})
}
