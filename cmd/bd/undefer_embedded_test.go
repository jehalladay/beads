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

func TestEmbeddedUndefer(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ud")

	// ===== Single Issue =====

	t.Run("undefer_single", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Undefer single", "--type", "task")
		bdDefer(t, bd, dir, issue.ID)
		if s := getIssueStatus(t, bd, dir, issue.ID); s != "deferred" {
			t.Fatalf("expected deferred before undefer, got %q", s)
		}

		out := bdUndefer(t, bd, dir, issue.ID)
		if !strings.Contains(out, "Undeferred") {
			t.Errorf("expected 'Undeferred' in output: %s", out)
		}
		if s := getIssueStatus(t, bd, dir, issue.ID); s != "open" {
			t.Errorf("expected status=open after undefer, got %q", s)
		}
	})

	t.Run("undefer_writes_gc_survivable_audit_trail", func(t *testing.T) {
		// beads-n4sn: undefer is a deferred->open status transition and must
		// write the GC-survivable audit-file entry via the shared chokepoint,
		// same as reopen/defer.
		issue := bdCreate(t, bd, dir, "Audit undefer", "--type", "task")
		bdDefer(t, bd, dir, issue.ID)
		bdUndefer(t, bd, dir, issue.ID)

		if !auditHasStatusChange(t, dir, issue.ID, "open") {
			t.Errorf("undefer did not write a GC-survivable audit field_change to status=open for %s (beads-n4sn)", issue.ID)
		}
	})

	// ===== Multiple Issues =====

	t.Run("undefer_multiple", func(t *testing.T) {
		issue1 := bdCreate(t, bd, dir, "Undefer multi 1", "--type", "task")
		issue2 := bdCreate(t, bd, dir, "Undefer multi 2", "--type", "task")
		bdDefer(t, bd, dir, issue1.ID, issue2.ID)

		out := bdUndefer(t, bd, dir, issue1.ID, issue2.ID)
		if !strings.Contains(out, issue1.ID) || !strings.Contains(out, issue2.ID) {
			t.Errorf("expected both IDs in output: %s", out)
		}
		for _, id := range []string{issue1.ID, issue2.ID} {
			if s := getIssueStatus(t, bd, dir, id); s != "open" {
				t.Errorf("expected %s status=open, got %q", id, s)
			}
		}
	})

	// ===== Not Deferred =====

	t.Run("undefer_not_deferred", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Undefer not deferred", "--type", "task")
		// Issue is open, not deferred — undefer should print error but not crash
		cmd := exec.Command(bd, "undefer", issue.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), "not deferred") {
			t.Errorf("expected 'not deferred' message: %s", out)
		}
	})

	// ===== All-failed guard (beads-7pcm) =====

	// When every requested ID fails to resolve, undefer must exit NON-ZERO
	// (nothing was undeferred) — not the previous unconditional rc=0 that made
	// scripts read false success on total failure. Mirrors defer (beads-0l4c).
	t.Run("undefer_all_failed_exit_nonzero", func(t *testing.T) {
		cmd := exec.Command(bd, "undefer", "ud-nope-aaa", "ud-nope-bbb")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		_, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Errorf("expected non-zero exit when all undefer IDs fail, got success\nstderr:\n%s", stderr.String())
		}
	})

	// All requested IDs fail under --json: stdout must carry a parseable JSON
	// error object, not be empty (beads-7pcm / beads-fg6 / beads-tx70).
	t.Run("undefer_all_failed_json_emits_stdout_error", func(t *testing.T) {
		cmd := exec.Command(bd, "undefer", "ud-nope-ccc", "ud-nope-ddd", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Errorf("expected non-zero exit when all IDs fail, got success\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		}
		out := strings.TrimSpace(stdout.String())
		if out == "" {
			t.Fatalf("stdout is empty on all-failed --json undefer — must emit a JSON error object (beads-7pcm)\nstderr:\n%s", stderr.String())
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not a JSON object on all-failed --json undefer: %v\nstdout:\n%s", jerr, out)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an \"error\" field in the all-failed --json stdout object, got: %s", out)
		}
	})

	// IDs that RESOLVE but all fail the deferred-status check (a valid, open,
	// not-deferred issue) reach the bottom guard: undeferredCount stays 0, so
	// the command must exit non-zero rather than the previous unconditional
	// rc=0 (beads-7pcm — this exercises the count-based guard, distinct from the
	// top-level unresolvable-ID path above).
	t.Run("undefer_resolved_but_none_deferred_exit_nonzero", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Open not deferred", "--type", "task")
		cmd := exec.Command(bd, "undefer", issue.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		_, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Errorf("expected non-zero exit when the only ID was not deferred, got success\nstderr:\n%s", stderr.String())
		}
	})
}

// TestEmbeddedUndeferConcurrent exercises undefer operations concurrently.
func TestEmbeddedUndeferConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ux")

	// Pre-create and defer issues
	var issueIDs []string
	for i := 0; i < 8; i++ {
		issue := bdCreate(t, bd, dir, fmt.Sprintf("undefer-concurrent-%d", i), "--type", "task")
		bdDefer(t, bd, dir, issue.ID)
		issueIDs = append(issueIDs, issue.ID)
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
			id := issueIDs[worker]

			cmd := exec.Command(bd, "undefer", id)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("undefer %s (worker %d): %v\n%s", id, worker, err, out)
			}
			results[worker] = r
		}(w)
	}
	wg.Wait()

	var successes int
	for _, r := range results {
		if r.err != nil {
			if !strings.Contains(r.err.Error(), "one writer at a time") {
				t.Errorf("worker %d failed: %v", r.worker, r.err)
			}
			continue
		}
		successes++
	}
	if successes == 0 {
		t.Fatal("all workers failed; expected at least 1 success")
	}
	t.Logf("%d/%d workers succeeded (flock contention expected)", successes, numWorkers)

	// Verify only successful workers' issues are open
	for _, r := range results {
		if r.err != nil {
			continue
		}
		id := issueIDs[r.worker]
		status := getIssueStatus(t, bd, dir, id)
		if status != "open" {
			t.Errorf("issue %d (%s): expected status=open, got %q", r.worker, id, status)
		}
	}
}
