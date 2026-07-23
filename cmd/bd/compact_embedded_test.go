//go:build cgo

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// bdCompact runs "bd compact" with the given args and returns stdout.
func bdCompact(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"compact"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd compact %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdCompactFail runs "bd compact" expecting failure.
func bdCompactFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"compact"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd compact %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

func TestEmbeddedCompact(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// ===== Dry Run =====

	t.Run("compact_dry_run", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cd")
		bdCreate(t, bd, dir, "Compact dry-run issue", "--type", "task")

		out := bdCompact(t, bd, dir, "--dry-run")
		if !strings.Contains(out, "DRY RUN") && !strings.Contains(out, "Nothing to compact") && !strings.Contains(out, "nothing to compact") {
			t.Errorf("expected dry-run or nothing-to-compact output: %s", out)
		}
	})

	// ===== Nothing to Compact (1 commit) =====

	t.Run("compact_nothing", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cn")
		out := bdCompact(t, bd, dir, "--force")
		if !strings.Contains(out, "Nothing to compact") && !strings.Contains(out, "nothing to compact") && !strings.Contains(out, "Only") {
			t.Errorf("expected nothing-to-compact message: %s", out)
		}
	})

	// ===== No --force Errors =====

	t.Run("compact_no_force", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cf")
		// Create issues to generate commits
		bdCreate(t, bd, dir, "Compact no-force 1", "--type", "task")
		bdCreate(t, bd, dir, "Compact no-force 2", "--type", "task")

		// With --days 0, all commits are "old"
		// May fail with "use --force" hint or succeed with "nothing to compact"
		cmd := exec.Command(bd, "compact", "--days", "0")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			// Succeeded — either nothing to compact or only 1 old commit
			_ = out
		} else {
			// Should contain --force hint
			if !strings.Contains(string(out), "--force") {
				t.Errorf("expected --force hint in error: %s", out)
			}
		}
	})

	// ===== Force with --days 0 =====

	t.Run("compact_force", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cx")
		// Create issues + config changes to build commit history
		bdCreate(t, bd, dir, "Compact force 1", "--type", "task")
		cmd := exec.Command(bd, "config", "set", "compact.test1", "v1")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.CombinedOutput()
		cmd = exec.Command(bd, "dolt", "commit", "-m", "config commit 1")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.CombinedOutput()
		bdCreate(t, bd, dir, "Compact force 2", "--type", "task")

		// Try compacting with --days 0
		out := bdCompact(t, bd, dir, "--force", "--days", "0")
		// Either compacts or reports nothing to compact — both OK
		if !strings.Contains(out, "Compacted") && !strings.Contains(out, "Nothing to compact") && !strings.Contains(out, "nothing to compact") && !strings.Contains(out, "Only") {
			t.Errorf("expected compact result: %s", out)
		}
	})

	// ===== --days Flag =====

	t.Run("compact_days_flag", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "dy")
		bdCreate(t, bd, dir, "Compact days issue", "--type", "task")

		out := bdCompact(t, bd, dir, "--dry-run", "--days", "7")
		if !strings.Contains(out, "7 days") && !strings.Contains(out, "Nothing to compact") && !strings.Contains(out, "nothing to compact") {
			t.Errorf("expected days reference in output: %s", out)
		}
	})

	// ===== JSON Output =====

	t.Run("compact_dry_run_json", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cj")
		bdCreate(t, bd, dir, "Compact JSON issue", "--type", "task")

		cmd := exec.Command(bd, "--json", "compact", "--dry-run")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd --json compact --dry-run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		// Should produce some output without crashing
		_ = stdout.String()
	})
}

// TestEmbeddedCompactPreservesUncommittedWorkingSet is the teeth for beads-f52cm:
// `bd compact --force` under batch/off Dolt auto-commit must NOT silently discard
// the uncommitted working set. The Compact recipe hard-resets main to a temp
// branch built only from committed history, so any working-set rows that were
// never committed used to vanish. The fix flushes the working set into a commit
// (compact_dolt.go, before store.Log) so it is folded into the compacted result.
//
// Repro (from the bead): batch mode, commit a few rows, leave one row
// uncommitted in the working set, run `bd compact --days 0 --force`, then assert
// the uncommitted row still lists. Pre-fix: it was gone.
func TestEmbeddedCompactPreservesUncommittedWorkingSet(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cw")

	// Build committed history: create rows and commit each so there is >1 old
	// commit for compact --days 0 to squash.
	doltCommit := func(msg string) {
		t.Helper()
		cmd := exec.Command(bd, "dolt", "commit", "-m", msg)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd dolt commit %q failed: %v\n%s", msg, err, out)
		}
	}

	bdCreate(t, bd, dir, "committed A", "--type", "task")
	doltCommit("c1")
	bdCreate(t, bd, dir, "committed B", "--type", "task")
	doltCommit("c2")
	bdCreate(t, bd, dir, "committed C", "--type", "task")
	doltCommit("c3")

	// Create an UNCOMMITTED row: run under batch auto-commit so the mutation stays
	// in the working set instead of being committed.
	uncommitted := func() *types.Issue {
		t.Helper()
		out, err := bdRunWithFlockRetry(t, bd, dir, "--dolt-auto-commit", "batch", "create", "--json", "UNCOMMITTED-ROW", "--type", "task")
		if err != nil {
			t.Fatalf("batch create failed: %v\n%s", err, out)
		}
		return parseIssueJSON(t, out)
	}()

	// Sanity: all 4 rows should be visible before compact (working set is part of
	// the queryable state).
	before := bdList(t, bd, dir, "--json")
	if !strings.Contains(before, uncommitted.ID) {
		t.Fatalf("precondition failed: uncommitted row %s not visible before compact:\n%s", uncommitted.ID, before)
	}

	// Compact all old history. Pre-fix this hard-reset main and dropped the
	// uncommitted row.
	out := bdCompact(t, bd, dir, "--days", "0", "--force")
	if !strings.Contains(out, "Compacted") && !strings.Contains(out, "nothing to compact") && !strings.Contains(out, "Nothing to compact") && !strings.Contains(out, "Only") {
		t.Fatalf("unexpected compact output: %s", out)
	}

	// THE ASSERTION: the uncommitted row must survive the compaction.
	after := bdList(t, bd, dir, "--json")
	if !strings.Contains(after, uncommitted.ID) {
		t.Errorf("beads-f52cm regression: uncommitted working-set row %s was DISCARDED by compact --force\nafter:\n%s", uncommitted.ID, after)
	}
}

// TestEmbeddedCompactConcurrent exercises compact --dry-run concurrently.
func TestEmbeddedCompactConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cc")

	bdCreate(t, bd, dir, "Compact concurrent issue", "--type", "task")

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
			cmd := exec.Command(bd, "compact", "--dry-run")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("compact --dry-run (worker %d): %v\n%s", worker, err, out)
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
