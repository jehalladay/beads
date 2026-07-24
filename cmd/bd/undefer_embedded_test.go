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

	// beads-36iz0: an ID that RESOLVES but is NOT deferred (a valid, open issue)
	// is an idempotent advisory no-op — undefer's target state (open) is already
	// satisfied — so it must exit rc=0, matching reopen's already-open path
	// (beads-hxc2) and defer's already-deferred no-op. This REVERSES the earlier
	// beads-7pcm count-based rc1 for this specific case: not-deferred is not a
	// failure. (The genuine unresolvable/not-found rc1 guard above is unchanged.)
	t.Run("undefer_resolved_but_not_deferred_is_rc0_noop", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Open not deferred", "--type", "task")
		cmd := exec.Command(bd, "undefer", issue.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Errorf("expected rc=0 for an already-not-deferred undefer no-op (beads-36iz0), got error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "not deferred") {
			t.Errorf("expected 'not deferred' advisory on stderr: %s", stderr.String())
		}
		// The no-op must not have flipped the status.
		if s := getIssueStatus(t, bd, dir, issue.ID); s != "open" {
			t.Errorf("expected status unchanged (open) after not-deferred no-op, got %q", s)
		}
	})

	// beads-36iz0: not-found is DISTINCT from not-deferred — it is a genuine
	// error and must still exit rc=1 (a script `bd undefer X || handle` should
	// fire on a typo/missing id, but NOT on an already-undeferred id).
	t.Run("undefer_not_found_still_rc1", func(t *testing.T) {
		cmd := exec.Command(bd, "undefer", "ud-nope-zzz")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		_, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Errorf("expected non-zero exit for an unresolvable undefer id (beads-36iz0), got success\nstderr:\n%s", stderr.String())
		}
	})

	// beads-36iz0: a mixed batch (one deferred + one not-deferred) is a full
	// success — the deferred one flips to open, the not-deferred one is a no-op,
	// and rc stays 0 (no genuine error occurred).
	t.Run("undefer_mixed_deferred_and_notdeferred_rc0", func(t *testing.T) {
		deferredIssue := bdCreate(t, bd, dir, "Mixed deferred", "--type", "task")
		bdDefer(t, bd, dir, deferredIssue.ID)
		openIssue := bdCreate(t, bd, dir, "Mixed open", "--type", "task")

		cmd := exec.Command(bd, "undefer", deferredIssue.ID, openIssue.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Errorf("expected rc=0 for a mixed deferred+not-deferred batch (beads-36iz0), got error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if s := getIssueStatus(t, bd, dir, deferredIssue.ID); s != "open" {
			t.Errorf("expected the deferred issue to flip to open, got %q", s)
		}
		if s := getIssueStatus(t, bd, dir, openIssue.ID); s != "open" {
			t.Errorf("expected the not-deferred issue to stay open, got %q", s)
		}
	})

	// beads-36iz0 (--json): a not-deferred-only batch is rc0 with EMPTY stdout
	// (no issues were undeferred, so no array) and the advisory flushed to stderr
	// as a JSON object (mirrors reopen's no-op-only --json tail, beads-en28). This
	// contrasts with the all-UNRESOLVABLE --json path above, which is rc1 + a
	// stdout JSON error object.
	t.Run("undefer_not_deferred_json_is_rc0_stderr_advisory", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Open not deferred json", "--type", "task")
		cmd := exec.Command(bd, "undefer", issue.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Errorf("expected rc=0 for a not-deferred-only --json undefer no-op (beads-36iz0), got error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if out := strings.TrimSpace(stdout.String()); out != "" {
			// stdout may legitimately be empty; if anything is present it must be
			// parseable JSON (never plain text).
			var v any
			if jerr := json.Unmarshal([]byte(out), &v); jerr != nil {
				t.Errorf("non-empty stdout on not-deferred --json no-op is not parseable JSON: %v\nstdout:\n%s", jerr, out)
			}
		}
		// The advisory on stderr must be a parseable JSON object, not plain text.
		se := strings.TrimSpace(stderr.String())
		if se != "" && strings.HasPrefix(se, "{") {
			var obj map[string]any
			if jerr := json.Unmarshal([]byte(se), &obj); jerr != nil {
				t.Errorf("stderr advisory under --json is not a parseable JSON object: %v\nstderr:\n%s", jerr, se)
			}
		}
	})
}

// TestEmbeddedUndeferInBatchDuplicateID guards beads-yn8r5: `bd undefer X X`
// (the same id repeated in one batch) must undefer the target exactly once and
// NOT emit a spurious "X is not deferred" advisory about the id the same command
// just undeferred. It is the in-batch-duplicate-id class sibling of beads-fwf0y
// (close), beads-4k0d8 (defer), and beads-qh4dy (update, fixed). Prior to the
// fix, undefer.go looped raw args with no dedup, so a repeated id undeferred on
// the first pass and then hit the not-deferred idempotent-no-op branch on the
// second pass — emitting a confusing false-negative advisory (text) and a
// phantom stderr item-error (--json) for a single logical target that fully
// succeeded.
func TestEmbeddedUndeferInBatchDuplicateID(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ub")

	t.Run("text_no_spurious_not_deferred_advisory", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "dup undefer text", "--type", "task")
		bdDefer(t, bd, dir, issue.ID)

		cmd := exec.Command(bd, "undefer", issue.ID, issue.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("undefer X X should succeed, got error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		combined := stdout.String() + stderr.String()
		// The id was undeferred by this same command — it must not then be
		// reported as "not deferred".
		if strings.Contains(combined, "not deferred") {
			t.Errorf("a repeated id produced a spurious 'not deferred' advisory for the id this command just undeferred:\n%s", combined)
		}
		// Exactly one "Undeferred" report for one logical target.
		if n := strings.Count(stdout.String(), "Undeferred"); n != 1 {
			t.Errorf("expected exactly 1 'Undeferred' report for a duplicated id, got %d\nstdout:\n%s", n, stdout.String())
		}
		if s := getIssueStatus(t, bd, dir, issue.ID); s != "open" {
			t.Errorf("expected status=open after undefer, got %q", s)
		}
	})

	t.Run("json_no_phantom_stderr_error", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "dup undefer json", "--type", "task")
		bdDefer(t, bd, dir, issue.ID)

		cmd := exec.Command(bd, "undefer", issue.ID, issue.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("undefer X X --json should succeed, got error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		// A single logical target that fully succeeded must not leak a phantom
		// per-item error to stderr.
		if strings.Contains(stderr.String(), "not deferred") {
			t.Errorf("a repeated id leaked a phantom 'not deferred' item-error to stderr under --json for a just-undeferred id:\n%s", stderr.String())
		}
		// stdout array should carry exactly one entry for one logical target.
		out := stdout.String()
		start := strings.Index(out, "[")
		if start < 0 {
			t.Fatalf("no JSON array in undefer --json stdout:\n%s", out)
		}
		var arr []map[string]interface{}
		if jerr := json.Unmarshal([]byte(out[start:]), &arr); jerr != nil {
			t.Fatalf("failed to parse undefer --json array: %v\nraw:\n%s", jerr, out[start:])
		}
		if len(arr) != 1 {
			t.Errorf("expected --json array length 1 for a duplicated id, got %d\nraw:\n%s", len(arr), out[start:])
		}
	})

	t.Run("distinct_ids_still_undefer_each", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "distinct undefer a", "--type", "task")
		b := bdCreate(t, bd, dir, "distinct undefer b", "--type", "task")
		bdDefer(t, bd, dir, a.ID, b.ID)

		out := bdUndefer(t, bd, dir, a.ID, b.ID)
		if !strings.Contains(out, a.ID) || !strings.Contains(out, b.ID) {
			t.Errorf("expected both distinct IDs in output: %s", out)
		}
		for _, id := range []string{a.ID, b.ID} {
			if s := getIssueStatus(t, bd, dir, id); s != "open" {
				t.Errorf("expected %s status=open, got %q", id, s)
			}
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
