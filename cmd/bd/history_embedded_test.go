//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// bdHistory runs "bd history" with the given args and returns raw stdout.
func bdHistory(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"history"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd history %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdHistoryFail runs "bd history" expecting failure.
func bdHistoryFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"history"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd history %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdHistoryJSON runs "bd history --json" and parses the result as a slice.
func bdHistoryJSON(t *testing.T, bd, dir string, args ...string) []map[string]interface{} {
	t.Helper()
	fullArgs := append([]string{"history", "--json"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd history --json %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "[")
	if start < 0 {
		return nil
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(s[start:]), &entries); err != nil {
		t.Fatalf("parse history JSON: %v\n%s", err, s)
	}
	return entries
}

func TestEmbeddedHistory(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hi")

	// Create an issue, then modify it several times to build history.
	issue := bdCreate(t, bd, dir, "History test issue", "--type", "task", "--priority", "3")
	bdUpdate(t, bd, dir, issue.ID, "--status", "in_progress")
	bdUpdate(t, bd, dir, issue.ID, "--priority", "1")
	bdUpdate(t, bd, dir, issue.ID, "--title", "History test issue updated")

	// ===== Basic history showing state changes =====

	t.Run("basic_history", func(t *testing.T) {
		out := bdHistory(t, bd, dir, issue.ID)
		if !strings.Contains(out, issue.ID) {
			t.Errorf("expected issue ID in history output: %s", out)
		}
		if !strings.Contains(out, "History for") {
			t.Errorf("expected 'History for' header: %s", out)
		}
		// The human view labels the Dolt committer honestly as "Committer:",
		// matching the --json "committer" field (beads-lf39) — NOT "Author:",
		// which misattributed every revision to the shared-bare-repo committer.
		if !strings.Contains(out, "Committer:") {
			t.Errorf("expected 'Committer:' in history output: %s", out)
		}
		if strings.Contains(out, "Author:") {
			t.Errorf("history human view should not mislabel the committer as 'Author:': %s", out)
		}
	})

	t.Run("history_shows_multiple_entries", func(t *testing.T) {
		entries := bdHistoryJSON(t, bd, dir, issue.ID)
		// We created + updated 3 times = at least 4 commits touching this issue
		if len(entries) < 4 {
			t.Errorf("expected at least 4 history entries, got %d", len(entries))
		}
	})

	// ===== --limit restricts entries =====

	t.Run("limit_restricts_entries", func(t *testing.T) {
		entries := bdHistoryJSON(t, bd, dir, issue.ID, "--limit", "2")
		if len(entries) > 2 {
			t.Errorf("expected at most 2 entries with --limit 2, got %d", len(entries))
		}
		if len(entries) == 0 {
			t.Error("expected at least 1 entry")
		}
	})

	t.Run("limit_1", func(t *testing.T) {
		entries := bdHistoryJSON(t, bd, dir, issue.ID, "--limit", "1")
		if len(entries) != 1 {
			t.Errorf("expected exactly 1 entry with --limit 1, got %d", len(entries))
		}
	})

	// ===== --limit header does not misreport page size as total (beads-qal3) =====

	// With >4 entries, `--limit 1` previously printed "History for <id> (1
	// entries)" — the truncated PAGE size, falsely implying only 1 entry
	// exists. The header must now qualify a truncated view as "showing K of N".
	t.Run("limit_header_reports_true_total", func(t *testing.T) {
		out := bdHistory(t, bd, dir, issue.ID, "--limit", "1")
		if !strings.Contains(out, "showing 1 of ") {
			t.Errorf("expected truncated header 'showing 1 of N entries', got: %s", out)
		}
		if strings.Contains(out, "(1 entries)") {
			t.Errorf("header must not report the --limit page size (1) as the total: %s", out)
		}
	})

	// Without --limit, the header reports the full count with no "showing" qualifier.
	t.Run("no_limit_header_unqualified", func(t *testing.T) {
		out := bdHistory(t, bd, dir, issue.ID)
		if strings.Contains(out, "showing ") {
			t.Errorf("un-truncated header must not say 'showing K of N': %s", out)
		}
	})

	// ===== Negative --limit fails loud (beads-4djp) =====

	// A negative --limit previously slipped past the `historyLimit > 0`
	// truncation guard, silently returning the FULL history with rc=0 — the
	// misleading false-green of the eqi4/r9hj negative-limit class. It must now
	// error loudly (the shared "--limit must be >= 0" contract), on both the
	// text and --json paths, rather than returning an unbounded set.
	t.Run("negative_limit_fails_loud", func(t *testing.T) {
		out := bdHistoryFail(t, bd, dir, issue.ID, "--limit", "-1")
		if !strings.Contains(out, "--limit must be >= 0") {
			t.Errorf("expected '--limit must be >= 0' error, got: %s", out)
		}
	})

	t.Run("negative_limit_json_fails_loud", func(t *testing.T) {
		cmd := exec.Command(bd, "history", "--json", "--limit", "-999", issue.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected nonzero exit for negative --limit, got success:\n%s", out)
		}
		s := strings.TrimSpace(string(out))
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
			t.Fatalf("expected a parseable JSON error object, got prose:\n%s\n(parse error: %v)", s, jerr)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an \"error\" field in the JSON error object, got: %s", s)
		}
	})

	// --limit 0 remains the documented "all" sentinel — must NOT error.
	t.Run("limit_zero_is_unlimited", func(t *testing.T) {
		entries := bdHistoryJSON(t, bd, dir, issue.ID, "--limit", "0")
		if len(entries) < 4 {
			t.Errorf("expected --limit 0 to return the full set (>=4), got %d", len(entries))
		}
	})

	// ===== --json output =====

	t.Run("json_output_structure", func(t *testing.T) {
		entries := bdHistoryJSON(t, bd, dir, issue.ID)
		if len(entries) == 0 {
			t.Fatal("expected non-empty history")
		}
		e := entries[0]
		// Check expected keys (snake_case per the --json field-name contract, beads-8slh)
		if _, ok := e["commit_hash"]; !ok {
			t.Errorf("expected 'commit_hash' key; got keys %v", mapKeys(e))
		}
		if _, ok := e["commit_date"]; !ok {
			t.Errorf("expected 'commit_date' key; got keys %v", mapKeys(e))
		}
		if _, ok := e["committer"]; !ok {
			t.Errorf("expected 'committer' key; got keys %v", mapKeys(e))
		}
		if _, ok := e["issue"]; !ok {
			t.Errorf("expected 'issue' key; got keys %v", mapKeys(e))
		}
	})

	t.Run("json_issue_snapshot_has_fields", func(t *testing.T) {
		entries := bdHistoryJSON(t, bd, dir, issue.ID)
		if len(entries) == 0 {
			t.Fatal("expected non-empty history")
		}
		// Most recent entry should have the updated title
		issueMap, ok := entries[0]["issue"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected issue to be a map, got %T", entries[0]["issue"])
		}
		if issueMap["title"] != "History test issue updated" {
			t.Errorf("expected latest title 'History test issue updated', got %v", issueMap["title"])
		}
	})

	// ===== Nonexistent issue ID =====

	// beads-4skk: a nonexistent issue must ERROR (rc!=0) with a "not found"
	// message, consistent with show/comments/children — not print "No history"
	// rc=0, which was indistinguishable from an existing issue with no history.
	t.Run("nonexistent_issue_errors", func(t *testing.T) {
		out := bdHistoryFail(t, bd, dir, "hi-nonexistent999")
		// The routed resolver reports a nonexistent id either as "no issue found
		// matching <id>" (resolver error) or "issue <id> not found" (nil result);
		// both are valid not-found signals and both exit rc!=0 (bdHistoryFail
		// already asserts non-zero). Accept either so the test tracks the real
		// resolver semantics rather than one exact phrasing (beads-4skk).
		if !strings.Contains(out, "not found") && !strings.Contains(out, "no issue found") {
			t.Errorf("expected a not-found error for nonexistent issue, got: %s", out)
		}
	})

	// --json must still produce parseable JSON on the error path (a JSON error
	// object, matching show/comments --json) so `bd history --json | jq` doesn't
	// break — but the process must exit non-zero (beads-4skk).
	t.Run("nonexistent_issue_json_errors_with_parseable_object", func(t *testing.T) {
		cmd := exec.Command(bd, "history", "--json", "hi-nonexistent999")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected nonzero exit for nonexistent issue, got success:\n%s", out)
		}
		s := strings.TrimSpace(string(out))
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
			t.Fatalf("expected a parseable JSON error object, got prose:\n%s\n(parse error: %v)", s, jerr)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an \"error\" field in the JSON error object, got: %s", s)
		}
	})

	// Same on the --limit path: still errors non-zero (the existence check runs
	// before limit handling).
	t.Run("nonexistent_issue_json_with_limit_errors", func(t *testing.T) {
		cmd := exec.Command(bd, "history", "--json", "--limit", "2", "hi-nonexistent999")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected nonzero exit for nonexistent issue with --limit, got:\n%s", out)
		}
	})

	// ===== Wrong number of args =====

	t.Run("no_args_fails", func(t *testing.T) {
		bdHistoryFail(t, bd, dir)
	})

	t.Run("too_many_args_fails", func(t *testing.T) {
		bdHistoryFail(t, bd, dir, issue.ID, "extra")
	})

	// ===== History for newly created issue =====

	t.Run("single_entry_for_new_issue", func(t *testing.T) {
		fresh := bdCreate(t, bd, dir, "Fresh issue no updates", "--type", "task")
		entries := bdHistoryJSON(t, bd, dir, fresh.ID)
		if len(entries) < 1 {
			t.Error("expected at least 1 history entry for a newly created issue")
		}
	})
}

// TestEmbeddedHistoryConcurrent exercises history operations concurrently.
func TestEmbeddedHistoryConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hc")

	// Create several issues with history.
	var ids []string
	for i := 0; i < 8; i++ {
		issue := bdCreate(t, bd, dir, fmt.Sprintf("concurrent-history-%d", i), "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--priority", "1")
		ids = append(ids, issue.ID)
	}

	const numWorkers = 8

	type workerResult struct {
		worker int
		err    error
	}

	results := make([]workerResult, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			r := workerResult{worker: worker}
			id := ids[worker]

			// JSON history
			args := []string{"history", "--json", id}
			cmd := exec.Command(bd, args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("worker %d history %s: %v\n%s", worker, id, err, out)
				results[worker] = r
				return
			}

			// Plain text history
			args = []string{"history", id, "--limit", "1"}
			cmd = exec.Command(bd, args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err = cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("worker %d history --limit 1: %v\n%s", worker, err, out)
				results[worker] = r
				return
			}

			results[worker] = r
		}(w)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil && !strings.Contains(r.err.Error(), "one writer at a time") {
			t.Errorf("worker %d failed: %v", r.worker, r.err)
		}
	}
}
