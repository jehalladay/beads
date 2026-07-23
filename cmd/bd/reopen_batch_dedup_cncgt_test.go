//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestReopenBatchDedup_cncgt pins the beads-cncgt fix: a repeated issue id in
// ONE `bd reopen X X` batch must be reported exactly once in the --json
// reopenedIssues array. Without the uniqueStrings(args) dedup at the reopen
// command entry (mirroring delete.go:86 + label add/remove hzg2y):
//
//   - iter-1 does the real closed->open reopen and appends the updated issue to
//     reopenedIssues;
//   - iter-2 re-resolves the SAME id, now sees it already-open, and appends it
//     AGAIN via the hxc2 already-open no-op success path.
//
// The DB stays correct (a single ReopenIssue write / one 'reopened' event), but
// the --json array double-counts one issue, misleading a script that counts
// reopened issues or reads the array. This is the repeated-ID batch class
// (hzg2y label / fwf0y close / qh4dy update) for the DISTINCT reopen verb.
// The dedup keeps first occurrence; genuinely-distinct ids are unaffected.
//
// Mutation check: remove `args = uniqueStrings(args)` in reopen.go's RunE and
// repeated_id_counts_once goes RED (the array has 2 entries for one issue).
func TestReopenBatchDedup_cncgt(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rc")

	closeIssue := func(t *testing.T, id string) {
		t.Helper()
		cmd := exec.Command(bd, "close", id, "--reason", "done")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("close %s: %v\n%s", id, err, out)
		}
	}

	// parseReopened extracts the []{id} JSON array `bd reopen --json` emits.
	type reopenedIssue struct {
		ID string `json:"id"`
	}
	runReopenJSON := func(t *testing.T, args ...string) []reopenedIssue {
		t.Helper()
		full := append([]string{"reopen"}, args...)
		full = append(full, "--json")
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd reopen %v failed: %v\n%s", args, err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "[")
		if start < 0 {
			t.Fatalf("no JSON array in reopen output: %s", s)
		}
		var res []reopenedIssue
		if err := json.Unmarshal([]byte(s[start:]), &res); err != nil {
			t.Fatalf("parse reopen JSON: %v\nstdout: %s", err, s)
		}
		return res
	}

	// A repeated id in one batch must appear exactly once.
	t.Run("repeated_id_counts_once", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "reopen dup id", "--type", "task")
		closeIssue(t, issue.ID)
		res := runReopenJSON(t, issue.ID, issue.ID)
		if len(res) != 1 {
			t.Fatalf("expected exactly 1 reopened entry for a repeated id, got %d: %+v", len(res), res)
		}
		if res[0].ID != issue.ID {
			t.Errorf("expected %s reopened, got %+v", issue.ID, res)
		}
	})

	// Negative control: two DISTINCT ids in one batch must both be reported.
	t.Run("distinct_ids_both_reported", func(t *testing.T) {
		i1 := bdCreate(t, bd, dir, "reopen distinct 1", "--type", "task")
		i2 := bdCreate(t, bd, dir, "reopen distinct 2", "--type", "task")
		closeIssue(t, i1.ID)
		closeIssue(t, i2.ID)
		res := runReopenJSON(t, i1.ID, i2.ID)
		if len(res) != 2 {
			t.Fatalf("expected 2 reopened entries for 2 distinct ids, got %d: %+v", len(res), res)
		}
		seen := map[string]bool{}
		for _, r := range res {
			seen[r.ID] = true
		}
		if !seen[i1.ID] || !seen[i2.ID] {
			t.Errorf("expected both %s and %s reported, got %+v", i1.ID, i2.ID, res)
		}
	})
}
