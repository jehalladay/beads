//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestDeferBatchDedup_4k0d8 pins the beads-4k0d8 fix: a repeated issue id in ONE
// `bd defer X X` batch must be handled exactly once. This is the in-batch-dup
// class (hzg2y label / fwf0y close / cncgt reopen / qh4dy update) for the defer
// verb. Without the uniqueStrings(args) dedup at defer's command entry:
//
//   - --json double-COUNTS: iter-1 does the real open->deferred write and appends
//     the issue to deferredIssues; iter-2 re-resolves the SAME id and appends it
//     AGAIN (the fs01 already-deferred guard only short-circuits the pure no-op
//     case deferUntil==nil && reason=="", so a 2nd occurrence with a mutating
//     flag like --until falls through and re-reports); and
//   - --reason double-APPENDS (real data duplication, dogfooder-verified): the
//     reason is written via store.AppendNotes per-id-occurrence inside the loop,
//     so `bd defer X X --reason foo` appends "foo" to the notes blob TWICE.
//
// The dedup keeps first occurrence; genuinely-distinct ids are unaffected.
//
// Mutation check: remove `args = uniqueStrings(args)` in defer.go's RunE and
// both repeated_id_* subtests go RED (json array has 2 entries for one issue;
// notes contain the reason token twice).
func TestDeferBatchDedup_4k0d8(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "df")

	// parseDeferred extracts the []{id} JSON array `bd defer --json` emits.
	type deferredIssue struct {
		ID string `json:"id"`
	}
	runDeferJSON := func(t *testing.T, args ...string) []deferredIssue {
		t.Helper()
		full := append([]string{"defer"}, args...)
		full = append(full, "--json")
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd defer %v failed: %v\n%s", args, err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "[")
		if start < 0 {
			t.Fatalf("no JSON array in defer output: %s", s)
		}
		var res []deferredIssue
		if err := json.Unmarshal([]byte(s[start:]), &res); err != nil {
			t.Fatalf("parse defer JSON: %v\nstdout: %s", err, s)
		}
		return res
	}

	// A repeated id with a mutating flag (--until) must appear exactly once in
	// the --json array (the double-report leg).
	t.Run("repeated_id_counts_once", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "defer dup id", "--type", "task")
		res := runDeferJSON(t, issue.ID, issue.ID, "--until", "2099-01-01")
		if len(res) != 1 {
			t.Fatalf("expected exactly 1 deferred entry for a repeated id, got %d: %+v", len(res), res)
		}
		if res[0].ID != issue.ID {
			t.Errorf("expected %s deferred, got %+v", issue.ID, res)
		}
	})

	// A repeated id with --reason must append the reason exactly once (the
	// severity-up real-data-duplication leg: store.AppendNotes ran per-occurrence).
	t.Run("repeated_id_reason_appended_once", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "defer dup reason", "--type", "task")
		const token = "UNIQUE-4k0d8-REASON-TOKEN"
		bdDefer(t, bd, dir, issue.ID, issue.ID, "--reason", token)
		got := bdShow(t, bd, dir, issue.ID)
		if n := strings.Count(got.Notes, token); n != 1 {
			t.Fatalf("expected reason token appended exactly once for a repeated id, got %d occurrences; notes=%q", n, got.Notes)
		}
	})

	// Negative control: two DISTINCT ids in one batch must both be reported.
	t.Run("distinct_ids_both_reported", func(t *testing.T) {
		i1 := bdCreate(t, bd, dir, "defer distinct 1", "--type", "task")
		i2 := bdCreate(t, bd, dir, "defer distinct 2", "--type", "task")
		res := runDeferJSON(t, i1.ID, i2.ID, "--until", "2099-01-01")
		if len(res) != 2 {
			t.Fatalf("expected 2 deferred entries for 2 distinct ids, got %d: %+v", len(res), res)
		}
		seen := map[string]bool{}
		for _, r := range res {
			seen[r.ID] = true
		}
		if !seen[i1.ID] || !seen[i2.ID] {
			t.Errorf("expected both %s and %s reported, got %+v", i1.ID, i2.ID, res)
		}
	})

	// beads-4k0d8/qvbjq COMPOSITION: the dedup MUST run BEFORE qvbjq's
	// count-mismatch guard. `bd defer X X -r a -r b` supplies 2 reasons for what
	// dedups to 1 DISTINCT id, so it must be REJECTED (2 != 1), not silently
	// collapsed to reason "a" with "b" dropped. The dedup placement in defer.go's
	// RunE is load-bearing but was untested by the subtests above (none exercise
	// 2-reasons-for-1-dup-id).
	//
	// Mutation check: move `args = uniqueStrings(args)` AFTER the
	// `len(reasons) > 1 && len(reasons) != len(args)` guard in defer.go and this
	// subtest goes RED — the guard sees len(args)==2==len(reasons), passes, then
	// the dedup shrinks args to 1 and the loop applies reasons[0]="a" only,
	// silently dropping "b" (the exact last-wins loss qvbjq fixed for distinct
	// IDs, reintroduced through the dup path).
	t.Run("repeated_id_two_reasons_rejected", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "defer dup two reasons", "--type", "task")
		out, err := deferCombined(bd, dir, issue.ID, issue.ID, "-r", "reason-a", "-r", "reason-b")
		if err == nil {
			t.Fatalf("2 reasons for a repeated (1 distinct) id must error, got success; out=%q", out)
		}
		if !strings.Contains(out, "2 defer reasons for 1 issue IDs") {
			t.Errorf("count-mismatch error should reflect the DEDUPED id count (1), got: %q", out)
		}
		// The issue must not have been deferred (validation precedes writes) and
		// must carry neither reason token.
		if got := bdShow(t, bd, dir, issue.ID); got.Status == "deferred" {
			t.Errorf("issue was deferred despite the count-mismatch error; status=%s", got.Status)
		} else if strings.Contains(got.Notes, "reason-a") || strings.Contains(got.Notes, "reason-b") {
			t.Errorf("no reason should have been appended on a rejected batch; notes=%q", got.Notes)
		}
	})
}
