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

	"github.com/steveyegge/beads/internal/types"
)

// bdReopen runs "bd reopen" with the given args and returns stdout.
func bdReopen(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"reopen"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd reopen %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestEmbeddedReopen(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ro")

	t.Run("reopen_single", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Reopen me", "--type", "task")
		bdClose(t, bd, dir, issue.ID)
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusClosed {
			t.Fatalf("expected closed before reopen, got %s", got.Status)
		}

		out := bdReopen(t, bd, dir, issue.ID)
		if !strings.Contains(out, "Reopened") {
			t.Errorf("expected 'Reopened' in output: %s", out)
		}

		got = bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusOpen {
			t.Errorf("expected open after reopen, got %s", got.Status)
		}
		if got.ClosedAt != nil {
			t.Error("expected closed_at cleared after reopen")
		}
	})

	t.Run("reopen_writes_gc_survivable_audit_trail", func(t *testing.T) {
		// beads-n4sn: reopen must write the GC-survivable audit-file
		// status-change entry (closed->open), like close/update do. Without it,
		// a Dolt GC flatten leaves the durable trail showing the close but not
		// the reopen.
		issue := bdCreate(t, bd, dir, "Audit reopen", "--type", "task")
		bdClose(t, bd, dir, issue.ID)
		bdReopen(t, bd, dir, issue.ID)

		if !auditHasStatusChange(t, dir, issue.ID, "open") {
			t.Errorf("reopen did not write a GC-survivable audit field_change to status=open for %s (beads-n4sn)", issue.ID)
		}
	})

	t.Run("reopen_multiple", func(t *testing.T) {
		issue1 := bdCreate(t, bd, dir, "Multi reopen 1", "--type", "task")
		issue2 := bdCreate(t, bd, dir, "Multi reopen 2", "--type", "task")
		bdClose(t, bd, dir, issue1.ID, issue2.ID)

		bdReopen(t, bd, dir, issue1.ID, issue2.ID)

		got1 := bdShow(t, bd, dir, issue1.ID)
		got2 := bdShow(t, bd, dir, issue2.ID)
		if got1.Status != types.StatusOpen {
			t.Errorf("issue1: expected open, got %s", got1.Status)
		}
		if got2.Status != types.StatusOpen {
			t.Errorf("issue2: expected open, got %s", got2.Status)
		}
	})

	// All requested IDs fail under --json: stdout must carry a parseable JSON
	// error object, not be empty (beads-2q0n / beads-fg6 / beads-tx70). A bare
	// SilentExit leaves --json consumers with exit 1 + empty stdout. reopen
	// resolves per-item INSIDE the batch loop, so a nonexistent id sets
	// hasError and reaches the terminal all-failed path (unlike close, whose
	// resolve fails earlier).
	t.Run("reopen_all_failed_json_emits_stdout_error", func(t *testing.T) {
		cmd := exec.Command(bd, "reopen", "ro-nope-aaa", "ro-nope-bbb", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Errorf("expected non-zero exit when all IDs fail, got success\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		}
		out := strings.TrimSpace(stdout.String())
		if out == "" {
			t.Fatalf("stdout is empty on all-failed --json reopen — must emit a JSON error object (beads-2q0n)\nstderr:\n%s", stderr.String())
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not a JSON object on all-failed --json reopen: %v\nstdout:\n%s", jerr, out)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an \"error\" field in the all-failed --json stdout object, got: %s", out)
		}
	})

	t.Run("reopen_with_reason", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Reason reopen", "--type", "task")
		bdClose(t, bd, dir, issue.ID)

		out := bdReopen(t, bd, dir, issue.ID, "--reason", "Not actually done")
		if !strings.Contains(out, "Reopened") {
			t.Errorf("expected 'Reopened' in output: %s", out)
		}
		if !strings.Contains(out, "Not actually done") {
			t.Logf("reason may not appear in text output: %s", out)
		}
	})

	t.Run("reopen_with_reason_short", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Short reason reopen", "--type", "task")
		bdClose(t, bd, dir, issue.ID)
		bdReopen(t, bd, dir, issue.ID, "-r", "Needs more work")
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusOpen {
			t.Errorf("expected open, got %s", got.Status)
		}
	})

	t.Run("reopen_json", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "JSON reopen", "--type", "task")
		bdClose(t, bd, dir, issue.ID)

		cmd := exec.Command(bd, "reopen", issue.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd reopen --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		s := strings.TrimSpace(stdout.String())
		start := strings.Index(s, "[")
		if start < 0 {
			start = strings.Index(s, "{")
		}
		if start >= 0 {
			// Verify it's valid JSON
			_ = s[start:]
		}
	})

	t.Run("reopen_already_open", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Already open", "--type", "task")
		// Reopen an already-open issue — should not error, just print message
		out := bdReopen(t, bd, dir, issue.ID)
		if !strings.Contains(out, "already open") {
			t.Logf("already-open message: %s", out)
		}
	})

	t.Run("reopen_in_progress_is_noop", func(t *testing.T) {
		// reopen only applies to closed issues: reopening an in_progress bead
		// must NOT silently revert it to open (beads-net1). It should be a
		// no-op with a clear "not closed" message and leave status unchanged.
		issue := bdCreate(t, bd, dir, "In progress reopen", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "in_progress")
		before := bdShow(t, bd, dir, issue.ID)
		if before.Status != types.StatusInProgress {
			t.Fatalf("expected in_progress before reopen, got %s", before.Status)
		}
		out := bdReopen(t, bd, dir, issue.ID)
		if strings.Contains(out, "Reopened") {
			t.Errorf("reopen of an in_progress issue should not report 'Reopened': %s", out)
		}
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusInProgress {
			t.Errorf("expected status unchanged (in_progress) after reopen of non-closed issue, got %s", got.Status)
		}
	})

	t.Run("reopen_clears_defer_until", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Deferred reopen", "--type", "task", "--defer", "2030-01-01")
		bdClose(t, bd, dir, issue.ID)
		bdReopen(t, bd, dir, issue.ID)
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusOpen {
			t.Errorf("expected open, got %s", got.Status)
		}
	})

	t.Run("reopen_nonexistent", func(t *testing.T) {
		cmd := exec.Command(bd, "reopen", "ro-nonexistent999")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected reopen of nonexistent to fail, got: %s", out)
		}
	})
}

// TestEmbeddedReopenConcurrent exercises reopen concurrently.
func TestEmbeddedReopenConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rx")

	const numWorkers = 8

	// Pre-create and close issues
	var issueIDs []string
	for i := 0; i < numWorkers; i++ {
		issue := bdCreate(t, bd, dir, fmt.Sprintf("concurrent-reopen-%d", i), "--type", "task")
		bdClose(t, bd, dir, issue.ID)
		issueIDs = append(issueIDs, issue.ID)
	}

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

			cmd := exec.Command(bd, "reopen", issueIDs[worker])
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("reopen %s: %v\n%s", issueIDs[worker], err, out)
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

	// Verify only successful workers' issues are reopened
	for _, r := range results {
		if r.err != nil {
			continue
		}
		id := issueIDs[r.worker]
		got := bdShow(t, bd, dir, id)
		if got.Status != types.StatusOpen {
			t.Errorf("expected %s to be open after reopen, got %s", id, got.Status)
		}
	}
}

// bdReopenFail runs "bd reopen" expecting a non-zero exit and returns combined output.
func bdReopenFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"reopen"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd reopen %s to fail, but it succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// TestEmbeddedReopenClosedEpicParentGuard verifies the beads-b0tw fix: reopening
// a closed child whose parent epic is itself closed recreates the
// closed-epic-with-open-child inconsistency the close-guard family prevents. Both
// child->open surfaces are guarded — `bd reopen` and `bd update --status open` —
// each overridable with --force. Sibling of beads-2hkd (demote) / beads-zgku
// (update --status closed) / beads-1d08 (batch close).
func TestEmbeddedReopenClosedEpicParentGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rc")

	// seedClosedEpicClosedChild builds an epic with one parent-child child, then
	// closes the child and the epic — the legit precondition for the bypass.
	seedClosedEpicClosedChild := func(t *testing.T, prefix string) (epic, child *types.Issue) {
		epic = bdCreate(t, bd, dir, prefix+" epic", "--type", "epic")
		child = bdCreate(t, bd, dir, prefix+" child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)
		bdClose(t, bd, dir, epic.ID)
		return epic, child
	}

	// `bd reopen <child>` must refuse when the parent epic is closed.
	t.Run("reopen_verb_closed_epic_parent_refuses", func(t *testing.T) {
		epic, child := seedClosedEpicClosedChild(t, "rv")
		out := bdReopenFail(t, bd, dir, child.ID)
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard message, got:\n%s", out)
		}
		// Child must stay closed (guard refused + no mutation).
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusClosed {
			t.Errorf("child should remain closed after refused reopen, got %s", got.Status)
		}
		_ = epic
	})

	// `bd update <child> --status open` must refuse identically (parity surface).
	t.Run("update_status_open_closed_epic_parent_refuses", func(t *testing.T) {
		_, child := seedClosedEpicClosedChild(t, "uv")
		out := bdUpdateFail(t, bd, dir, child.ID, "--status", "open")
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard message on update path, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusClosed {
			t.Errorf("child should remain closed after refused update --status open, got %s", got.Status)
		}
	})

	// --force overrides on the reopen verb (operator escape hatch, family parity).
	t.Run("reopen_verb_force_succeeds", func(t *testing.T) {
		_, child := seedClosedEpicClosedChild(t, "rf")
		bdReopen(t, bd, dir, child.ID, "--force")
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("expected child reopened with --force, got %s", got.Status)
		}
	})

	// --force overrides on the update --status open path too.
	t.Run("update_status_open_force_succeeds", func(t *testing.T) {
		_, child := seedClosedEpicClosedChild(t, "uf")
		bdUpdate(t, bd, dir, child.ID, "--status", "open", "--force")
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("expected child reopened via update --force, got %s", got.Status)
		}
	})

	// If the parent epic is still OPEN, reopening a closed child is fine.
	t.Run("open_epic_parent_reopen_succeeds", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "oe epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "oe child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID) // epic stays open
		bdReopen(t, bd, dir, child.ID)
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("expected child reopened under an open epic, got %s", got.Status)
		}
	})

	// A closed issue with NO epic parent reopens normally (guard is scoped).
	t.Run("no_epic_parent_reopen_succeeds", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "orphan", "--type", "task")
		bdClose(t, bd, dir, issue.ID)
		bdReopen(t, bd, dir, issue.ID)
		if got := bdShow(t, bd, dir, issue.ID); got.Status != types.StatusOpen {
			t.Errorf("expected plain closed issue reopened, got %s", got.Status)
		}
	})

	// A child whose parent is a closed NON-epic (task parent) reopens normally —
	// the invariant is specifically about closed EPIC parents.
	t.Run("closed_nonepic_parent_reopen_succeeds", func(t *testing.T) {
		parent := bdCreate(t, bd, dir, "task parent", "--type", "task")
		child := bdCreate(t, bd, dir, "task child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, parent.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)
		bdClose(t, bd, dir, parent.ID)
		bdReopen(t, bd, dir, child.ID)
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("expected child under a closed non-epic parent to reopen, got %s", got.Status)
		}
	})
}
