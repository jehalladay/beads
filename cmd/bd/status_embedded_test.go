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

// bdStatus runs "bd status" with the given args and returns raw stdout.
func bdStatus(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"status"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd status %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdStatusJSON runs "bd status --json" and parses the result.
func bdStatusJSON(t *testing.T, bd, dir string, args ...string) map[string]interface{} {
	t.Helper()
	fullArgs := append([]string{"status", "--json"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd status --json %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("no JSON object in status output: %s", s)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s[start:]), &m); err != nil {
		t.Fatalf("parse status JSON: %v\n%s", err, s)
	}
	return m
}

func TestEmbeddedStatus(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ss")

	// Create a known set of issues with specific statuses.
	bdCreate(t, bd, dir, "Status open 1", "--type", "task")
	bdCreate(t, bd, dir, "Status open 2", "--type", "bug")
	ip := bdCreate(t, bd, dir, "Status in_progress", "--type", "task", "--assignee", "alice")
	bdUpdate(t, bd, dir, ip.ID, "--status", "in_progress")
	closed := bdCreate(t, bd, dir, "Status closed", "--type", "task")
	bdClose(t, bd, dir, closed.ID)
	bdCreate(t, bd, dir, "Status assigned bob", "--type", "task", "--assignee", "bob")

	// ===== Default statistics output =====

	t.Run("default_output", func(t *testing.T) {
		out := bdStatus(t, bd, dir)
		if !strings.Contains(out, "Issue Database Status") {
			t.Errorf("expected 'Issue Database Status' header: %s", out)
		}
		if !strings.Contains(out, "Total Issues:") {
			t.Errorf("expected 'Total Issues:' in output: %s", out)
		}
		if !strings.Contains(out, "Open:") {
			t.Errorf("expected 'Open:' in output: %s", out)
		}
	})

	// ===== --json output =====

	t.Run("json_output_structure", func(t *testing.T) {
		m := bdStatusJSON(t, bd, dir)
		summary, ok := m["summary"].(map[string]interface{})
		if !ok {
			t.Fatal("expected 'summary' object in JSON output")
		}
		for _, key := range []string{"total_issues", "open_issues", "in_progress_issues", "closed_issues"} {
			if _, ok := summary[key]; !ok {
				t.Errorf("expected '%s' key in summary", key)
			}
		}
	})

	t.Run("json_counts_match", func(t *testing.T) {
		m := bdStatusJSON(t, bd, dir)
		summary := m["summary"].(map[string]interface{})
		total := int(summary["total_issues"].(float64))
		if total < 5 {
			t.Errorf("expected at least 5 total issues, got %d", total)
		}
		open := int(summary["open_issues"].(float64))
		if open < 3 {
			t.Errorf("expected at least 3 open issues, got %d", open)
		}
		inProgress := int(summary["in_progress_issues"].(float64))
		if inProgress < 1 {
			t.Errorf("expected at least 1 in_progress issue, got %d", inProgress)
		}
		closedCount := int(summary["closed_issues"].(float64))
		if closedCount < 1 {
			t.Errorf("expected at least 1 closed issue, got %d", closedCount)
		}
	})

	// ===== deferred is visible in the summary (beads-2pzw) =====

	t.Run("deferred_shown_in_summary", func(t *testing.T) {
		// A deferred issue was previously counted in Total but omitted from
		// every rendered bucket, so Open+InProgress+Closed did not reconcile to
		// Total. Defer a fresh issue and assert the human summary surfaces it.
		def := bdCreate(t, bd, dir, "To be deferred", "--type", "task")
		if out, err := bdRunWithFlockRetry(t, bd, dir, "defer", def.ID); err != nil {
			t.Fatalf("bd defer failed: %v\n%s", err, out)
		}

		out := bdStatus(t, bd, dir)
		if !strings.Contains(out, "Deferred:") {
			t.Errorf("expected 'Deferred:' in status summary once a deferred issue exists (beads-2pzw), got:\n%s", out)
		}

		// JSON already exposed deferred_issues; assert it is non-zero so the
		// human line and the JSON field agree.
		m := bdStatusJSON(t, bd, dir)
		summary := m["summary"].(map[string]interface{})
		if dv, ok := summary["deferred_issues"].(float64); !ok || int(dv) < 1 {
			t.Errorf("expected deferred_issues >= 1 in JSON summary, got %v", summary["deferred_issues"])
		}
	})

	// ===== hooked counts as in-progress in the summary (beads-2pzw leg 2) =====

	t.Run("hooked_counted_as_in_progress", func(t *testing.T) {
		// 'hooked' is CategoryWIP (a worker attached the bead to its hook) and is
		// displayed as in-progress elsewhere, but was counted in Total and NO
		// summary bucket. Assert a hooked issue lands in in_progress_issues.
		before := int(bdStatusJSON(t, bd, dir)["summary"].(map[string]interface{})["in_progress_issues"].(float64))

		h := bdCreate(t, bd, dir, "To be hooked", "--type", "task")
		if out, err := bdRunWithFlockRetry(t, bd, dir, "update", h.ID, "--status", "hooked"); err != nil {
			t.Fatalf("bd update --status hooked failed: %v\n%s", err, out)
		}

		after := int(bdStatusJSON(t, bd, dir)["summary"].(map[string]interface{})["in_progress_issues"].(float64))
		if after != before+1 {
			t.Errorf("hooking an issue should raise in_progress_issues by 1 (beads-2pzw): before=%d after=%d", before, after)
		}
	})

	// ===== --assigned =====

	t.Run("assigned_filter", func(t *testing.T) {
		// Set up env with a known git user
		env := bdEnv(dir)
		env = append(env, "GIT_AUTHOR_EMAIL=alice@test.com")

		args := []string{"status", "--json", "--assigned"}
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = env
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd status --assigned --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}

		s := strings.TrimSpace(stdout.String())
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON: %s", s)
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(s[start:]), &m); err != nil {
			t.Fatalf("parse: %v", err)
		}
		summary := m["summary"].(map[string]interface{})
		total := int(summary["total_issues"].(float64))
		// alice has 1 issue assigned
		if total > 5 {
			t.Errorf("assigned should filter to fewer issues, got total=%d", total)
		}
	})

	// ===== --no-activity =====

	t.Run("no_activity_flag", func(t *testing.T) {
		m := bdStatusJSON(t, bd, dir, "--no-activity")
		// Should still have summary, just no recent_activity
		if _, ok := m["summary"]; !ok {
			t.Error("expected 'summary' even with --no-activity")
		}
		// recent_activity should be null/absent
		if activity, ok := m["recent_activity"]; ok && activity != nil {
			t.Error("expected no recent_activity with --no-activity")
		}
	})

	// ===== Empty database =====

	t.Run("empty_database", func(t *testing.T) {
		emptyDir, _, _ := bdInit(t, bd, "--prefix", "ss2")
		m := bdStatusJSON(t, bd, emptyDir)
		summary := m["summary"].(map[string]interface{})
		total := int(summary["total_issues"].(float64))
		if total != 0 {
			t.Errorf("expected 0 total issues in empty db, got %d", total)
		}
	})

	// ===== stats alias =====

	t.Run("stats_alias", func(t *testing.T) {
		cmd := exec.Command(bd, "stats", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd stats alias failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "summary") {
			t.Errorf("expected 'summary' in stats alias output: %s", stdout.String())
		}
	})

	// ===== Human-readable sections =====

	t.Run("human_readable_sections", func(t *testing.T) {
		out := bdStatus(t, bd, dir)
		if !strings.Contains(out, "Summary:") {
			t.Errorf("expected 'Summary:' section: %s", out)
		}
		if !strings.Contains(out, "In Progress:") {
			t.Errorf("expected 'In Progress:' line: %s", out)
		}
		if !strings.Contains(out, "Ready to Work:") {
			t.Errorf("expected 'Ready to Work:' line: %s", out)
		}
	})
}

// TestEmbeddedStatusConcurrent exercises status operations concurrently.
func TestEmbeddedStatusConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ssc")

	for i := 0; i < 10; i++ {
		bdCreate(t, bd, dir, fmt.Sprintf("concurrent-status-%d", i), "--type", "task")
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

			queries := [][]string{
				{"--json"},
				{"--json", "--no-activity"},
				{"--json"},
				{"--json", "--no-activity"},
				{"--json"},
				{"--json"},
				{"--json", "--no-activity"},
				{"--json"},
			}
			q := queries[worker%len(queries)]

			args := append([]string{"status"}, q...)
			cmd := exec.Command(bd, args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err != nil {
				r.err = fmt.Errorf("worker %d status: %v\nstdout:\n%s\nstderr:\n%s", worker, err, stdout.String(), stderr.String())
				results[worker] = r
				return
			}

			// Verify JSON parses
			s := strings.TrimSpace(stdout.String())
			start := strings.Index(s, "{")
			if start < 0 {
				r.err = fmt.Errorf("worker %d: no JSON: %s", worker, s)
				results[worker] = r
				return
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(s[start:]), &m); err != nil {
				r.err = fmt.Errorf("worker %d: JSON parse: %v", worker, err)
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
