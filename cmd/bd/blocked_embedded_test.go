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

func TestEmbeddedBlocked(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bl")

	// ===== Default Empty =====

	t.Run("blocked_default_empty", func(t *testing.T) {
		cmd := exec.Command(bd, "blocked")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd blocked failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		// No blocked issues on fresh db
		_ = stdout.String()
	})

	// ===== With Blocked Issue =====

	t.Run("blocked_with_issue", func(t *testing.T) {
		blocker := bdCreate(t, bd, dir, "Blocker for blocked test", "--type", "task")
		blocked := bdCreate(t, bd, dir, "I am blocked", "--type", "task")

		// blocked depends on blocker (blocker blocks blocked)
		cmd := exec.Command(bd, "dep", "add", blocked.ID, blocker.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add failed: %v\n%s", err, out)
		}

		cmd = exec.Command(bd, "blocked")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd blocked failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), blocked.ID) {
			t.Errorf("expected %s in blocked output: %s", blocked.ID, stdout.String())
		}
	})

	// ===== --json =====

	t.Run("blocked_json", func(t *testing.T) {
		cmd := exec.Command(bd, "blocked", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd blocked --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		s := strings.TrimSpace(stdout.String())
		start := strings.IndexAny(s, "[{")
		if start < 0 {
			t.Fatalf("no JSON in blocked --json output: %s", s)
		}
		if !json.Valid([]byte(s[start:])) {
			t.Errorf("invalid JSON in blocked output: %s", s[:min(200, len(s))])
		}
	})
}

func TestEmbeddedBlockedConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bx")

	bdCreate(t, bd, dir, "Blocked concurrent issue", "--type", "task")

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
			cmd := exec.Command(bd, "blocked")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("blocked (worker %d): %v\n%s", worker, err, out)
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

// TestEmbeddedBlockedParentExistenceCheck is the beads-d5jg teeth: bd blocked
// --parent <NONEXISTENT> must error (rc!=0, "not found") like bd list --parent
// (beads-n8lv), not silently return [] exit 0 — a typo'd epic id in a
// "what's blocked under this epic" gate should be a hard error, not read as
// "nothing blocked". Existence-axis twin of beads-lxo5 (recursion) on the same
// command.
func TestEmbeddedBlockedParentExistenceCheck(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bpe")
	epic := bdCreate(t, bd, dir, "real epic", "--type", "epic")

	// Nonexistent parent must error, in both text and --json.
	for _, args := range [][]string{
		{"blocked", "--parent", "bpe-nonexistent"},
		{"blocked", "--parent", "bpe-nonexistent", "--json"},
	} {
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("bd %v: expected non-zero exit for nonexistent parent, got success:\n%s", args, out)
		}
		if !strings.Contains(string(out), "not found") {
			t.Errorf("bd %v: expected 'not found' error, got:\n%s", args, out)
		}
	}

	// A real, childless parent must NOT error — it's a valid query with an
	// empty result (surgical: the guard only rejects missing parents).
	cmd := exec.Command(bd, "blocked", "--parent", epic.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd blocked --parent %s (valid childless): expected success, got %v:\n%s", epic.ID, err, out)
	}
}

// TestEmbeddedBlockedAssigneeFilter is the beads-x5c76 teeth: bd blocked --assignee
// must filter blocked issues by assignee, at parity with bd ready --assignee and
// bd list --assignee. Before the fix bd blocked had no --assignee flag, so
// "what of MINE is blocked?" was unexpressable. Asserts (1) --assignee alice
// returns ONLY alice's blocked issue and excludes bob's, (2) case-insensitive
// match (mirrors the ready LOWER(assignee)=LOWER(?) convention), (3) --json
// carries the same filtered set.
func TestEmbeddedBlockedAssigneeFilter(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "baf")

	blocker := bdCreate(t, bd, dir, "shared blocker", "--type", "task")
	blockedAlice := bdCreate(t, bd, dir, "alice blocked", "--type", "task", "--assignee", "alice")
	blockedBob := bdCreate(t, bd, dir, "bob blocked", "--type", "task", "--assignee", "bob")

	for _, dep := range [][]string{
		{"dep", "add", blockedAlice.ID, blocker.ID},
		{"dep", "add", blockedBob.ID, blocker.ID},
	} {
		cmd := exec.Command(bd, dep...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %v failed: %v\n%s", dep, err, out)
		}
	}

	// --assignee alice: alice's blocked issue present, bob's absent.
	cmd := exec.Command(bd, "blocked", "--assignee", "alice")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --assignee alice failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), blockedAlice.ID) {
		t.Errorf("expected alice's blocked %s in --assignee alice output:\n%s", blockedAlice.ID, stdout.String())
	}
	if strings.Contains(stdout.String(), blockedBob.ID) {
		t.Errorf("bob's blocked %s leaked into --assignee alice output:\n%s", blockedBob.ID, stdout.String())
	}

	// Case-insensitive: --assignee ALICE still matches (LOWER=LOWER convention).
	cmd = exec.Command(bd, "blocked", "--assignee", "ALICE")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err = runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --assignee ALICE failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), blockedAlice.ID) {
		t.Errorf("case-insensitive --assignee ALICE should match alice; got:\n%s", stdout.String())
	}

	// --json carries the same filtered set: exactly alice's, not bob's.
	cmd = exec.Command(bd, "blocked", "--assignee", "alice", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err = runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --assignee alice --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		t.Fatalf("no JSON in blocked --assignee --json output: %s", s)
	}
	var blocked []*types.BlockedIssue
	if jerr := json.Unmarshal([]byte(s[start:]), &blocked); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, s[start:])
	}
	for _, b := range blocked {
		if b.ID == blockedBob.ID {
			t.Errorf("bob's blocked %s leaked into --assignee alice --json", blockedBob.ID)
		}
		if !strings.EqualFold(b.Assignee, "alice") {
			t.Errorf("non-alice issue %s (assignee=%q) in --assignee alice --json", b.ID, b.Assignee)
		}
	}
}

// beads-9tljp: bd blocked --unassigned filters to blocked work that has NO
// owner — the triage complement of x5c76's --assignee ("what blocked work
// needs assigning?"), at parity with bd ready --unassigned. Asserts (1) an
// unassigned blocked issue is present, (2) an assigned blocked issue is
// excluded, (3) --json carries the same filtered set, (4) the mutual-exclusion
// precedence (ready.go:288) — --unassigned wins over --assignee, so
// `--unassigned --assignee bob` still returns only the unassigned issue.
func TestEmbeddedBlockedUnassignedFilter(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "buf")

	blocker := bdCreate(t, bd, dir, "shared blocker", "--type", "task")
	blockedOrphan := bdCreate(t, bd, dir, "unowned blocked", "--type", "task")
	blockedBob := bdCreate(t, bd, dir, "bob blocked", "--type", "task", "--assignee", "bob")

	for _, dep := range [][]string{
		{"dep", "add", blockedOrphan.ID, blocker.ID},
		{"dep", "add", blockedBob.ID, blocker.ID},
	} {
		cmd := exec.Command(bd, dep...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %v failed: %v\n%s", dep, err, out)
		}
	}

	// --unassigned: the unowned blocked issue present, the assigned one absent.
	cmd := exec.Command(bd, "blocked", "--unassigned")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --unassigned failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), blockedOrphan.ID) {
		t.Errorf("expected unowned blocked %s in --unassigned output:\n%s", blockedOrphan.ID, stdout.String())
	}
	if strings.Contains(stdout.String(), blockedBob.ID) {
		t.Errorf("assigned blocked %s leaked into --unassigned output:\n%s", blockedBob.ID, stdout.String())
	}

	// Mutual-exclusion (ready.go:288 mirror): --unassigned wins over --assignee.
	cmd = exec.Command(bd, "blocked", "--unassigned", "--assignee", "bob")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err = runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --unassigned --assignee bob failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), blockedOrphan.ID) {
		t.Errorf("--unassigned should win over --assignee bob; expected unowned %s:\n%s", blockedOrphan.ID, stdout.String())
	}
	if strings.Contains(stdout.String(), blockedBob.ID) {
		t.Errorf("--unassigned should win over --assignee bob; bob's %s must not appear:\n%s", blockedBob.ID, stdout.String())
	}

	// --json carries the same filtered set: the unowned one, none with an owner.
	cmd = exec.Command(bd, "blocked", "--unassigned", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err = runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --unassigned --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		t.Fatalf("no JSON in blocked --unassigned --json output: %s", s)
	}
	var blocked []*types.BlockedIssue
	if jerr := json.Unmarshal([]byte(s[start:]), &blocked); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, s[start:])
	}
	sawOrphan := false
	for _, b := range blocked {
		if strings.TrimSpace(b.Assignee) != "" {
			t.Errorf("assigned issue %s (assignee=%q) in --unassigned --json", b.ID, b.Assignee)
		}
		if b.ID == blockedOrphan.ID {
			sawOrphan = true
		}
	}
	if !sawOrphan {
		t.Errorf("unowned blocked %s missing from --unassigned --json:\n%s", blockedOrphan.ID, s[start:])
	}
}

// beads-o7nxb: bd blocked --priority / --type filter parity with bd ready
// (triage lenses "what P0/P1 work is blocked?" / "what bugs are blocked?").
// Before the fix bd blocked exposed neither, so a blocked-work triage by
// priority or type was unexpressable. Asserts (1) --priority N returns only
// blocked issues of that priority, (2) --type T returns only that type,
// (3) an invalid --type hard-errors (mirrors ready's gddf validation) rather
// than silently matching nothing, (4) --json carries the same filtered set.
func TestEmbeddedBlockedPriorityTypeFilter(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bpt")

	blocker := bdCreate(t, bd, dir, "shared blocker", "--type", "task")
	// P0 bug, P3 task — two distinct (priority,type) blocked issues.
	blockedP0Bug := bdCreate(t, bd, dir, "urgent bug blocked", "--type", "bug", "--priority", "0")
	blockedP3Task := bdCreate(t, bd, dir, "routine task blocked", "--type", "task", "--priority", "3")

	for _, dep := range [][]string{
		{"dep", "add", blockedP0Bug.ID, blocker.ID},
		{"dep", "add", blockedP3Task.ID, blocker.ID},
	} {
		cmd := exec.Command(bd, dep...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %v failed: %v\n%s", dep, err, out)
		}
	}

	// --priority 0: the P0 bug present, the P3 task absent.
	cmd := exec.Command(bd, "blocked", "--priority", "0")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --priority 0 failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), blockedP0Bug.ID) {
		t.Errorf("expected P0 %s in --priority 0 output:\n%s", blockedP0Bug.ID, stdout.String())
	}
	if strings.Contains(stdout.String(), blockedP3Task.ID) {
		t.Errorf("P3 %s leaked into --priority 0 output:\n%s", blockedP3Task.ID, stdout.String())
	}

	// --type bug: the bug present, the task absent.
	cmd = exec.Command(bd, "blocked", "--type", "bug")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err = runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --type bug failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), blockedP0Bug.ID) {
		t.Errorf("expected bug %s in --type bug output:\n%s", blockedP0Bug.ID, stdout.String())
	}
	if strings.Contains(stdout.String(), blockedP3Task.ID) {
		t.Errorf("task %s leaked into --type bug output:\n%s", blockedP3Task.ID, stdout.String())
	}

	// Invalid --type hard-errors (mirrors ready gddf), not a silent empty result.
	cmd = exec.Command(bd, "blocked", "--type", "bogustype")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bd blocked --type bogustype: expected non-zero exit for invalid type, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "invalid issue type") {
		t.Errorf("bd blocked --type bogustype: expected 'invalid issue type' error, got:\n%s", out)
	}

	// --json carries the same filtered set for --type bug: the bug, not the task.
	cmd = exec.Command(bd, "blocked", "--type", "bug", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err = runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd blocked --type bug --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		t.Fatalf("no JSON in blocked --type bug --json output: %s", s)
	}
	var blocked []*types.BlockedIssue
	if jerr := json.Unmarshal([]byte(s[start:]), &blocked); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, s[start:])
	}
	sawBug := false
	for _, b := range blocked {
		if b.ID == blockedP3Task.ID {
			t.Errorf("task %s leaked into --type bug --json", blockedP3Task.ID)
		}
		if b.ID == blockedP0Bug.ID {
			sawBug = true
		}
	}
	if !sawBug {
		t.Errorf("bug %s missing from --type bug --json:\n%s", blockedP0Bug.ID, s[start:])
	}
}
