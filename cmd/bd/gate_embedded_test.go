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

// bdGate runs "bd gate" with the given args and returns stdout.
func bdGate(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"gate"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd gate %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdGateFail runs "bd gate" expecting failure.
func bdGateFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"gate"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd gate %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdGateListJSON runs "bd gate list --json" and returns parsed results.
func bdGateListJSON(t *testing.T, bd, dir string, args ...string) []map[string]interface{} {
	t.Helper()
	fullArgs := append([]string{"gate", "list", "--json"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd gate list --json %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "[")
	if start < 0 {
		return nil
	}
	var results []map[string]interface{}
	if err := json.Unmarshal([]byte(s[start:]), &results); err != nil {
		t.Fatalf("parse gate list JSON: %v\n%s", err, s)
	}
	return results
}

// createGate creates a gate issue and returns it.
func createGate(t *testing.T, bd, dir, title string, extraArgs ...string) *types.Issue {
	t.Helper()
	args := append([]string{title, "--type", "gate"}, extraArgs...)
	return bdCreate(t, bd, dir, args...)
}

func TestEmbeddedGate(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "tg")

	// Register "gate" as a custom type so bd create --type gate works.
	store := openStore(t, beadsDir, "tg")
	if err := store.SetConfig(t.Context(), "types.custom", `["gate"]`); err != nil {
		t.Fatalf("SetConfig types.custom: %v", err)
	}
	store.Close()

	// ===== Gate List =====

	t.Run("gate_list_empty", func(t *testing.T) {
		out := bdGate(t, bd, dir, "list")
		if !strings.Contains(out, "No gates") {
			t.Logf("expected 'No gates' message: %s", out)
		}
	})

	t.Run("gate_list_shows_open_gates", func(t *testing.T) {
		gate := createGate(t, bd, dir, "List test gate")
		out := bdGate(t, bd, dir, "list")
		if !strings.Contains(out, gate.ID) {
			t.Errorf("expected gate %s in list output: %s", gate.ID, out)
		}
	})

	t.Run("gate_list_json", func(t *testing.T) {
		results := bdGateListJSON(t, bd, dir)
		if len(results) == 0 {
			t.Error("expected at least one gate in JSON list")
		}
	})

	t.Run("gate_list_excludes_closed_by_default", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Close me gate")
		bdGate(t, bd, dir, "resolve", gate.ID)
		results := bdGateListJSON(t, bd, dir)
		for _, r := range results {
			if r["id"] == gate.ID {
				t.Errorf("closed gate %s should not appear without --all", gate.ID)
			}
		}
	})

	t.Run("gate_list_all_includes_closed", func(t *testing.T) {
		gate := createGate(t, bd, dir, "All flag gate")
		bdGate(t, bd, dir, "resolve", gate.ID)
		results := bdGateListJSON(t, bd, dir, "--all")
		found := false
		for _, r := range results {
			if r["id"] == gate.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("expected closed gate %s with --all flag", gate.ID)
		}
	})

	t.Run("gate_list_limit", func(t *testing.T) {
		// Create several gates
		for i := 0; i < 3; i++ {
			createGate(t, bd, dir, fmt.Sprintf("Limit gate %d", i))
		}
		results := bdGateListJSON(t, bd, dir, "--limit", "1")
		if len(results) > 1 {
			t.Errorf("expected at most 1 result with --limit 1, got %d", len(results))
		}
	})

	// ===== Gate Show =====

	t.Run("gate_show", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Show gate", "--description", "Gate description")
		out := bdGate(t, bd, dir, "show", gate.ID)
		if !strings.Contains(out, gate.ID) {
			t.Errorf("expected gate ID in show output: %s", out)
		}
		if !strings.Contains(out, "Show gate") {
			t.Errorf("expected gate title in show output: %s", out)
		}
	})

	t.Run("gate_show_nonexistent", func(t *testing.T) {
		bdGateFail(t, bd, dir, "show", "tg-nonexistent999")
	})

	t.Run("gate_show_not_a_gate", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Not a gate", "--type", "task")
		bdGateFail(t, bd, dir, "show", task.ID)
	})

	// ===== Gate Resolve =====

	t.Run("gate_resolve", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Resolve me")
		out := bdGate(t, bd, dir, "resolve", gate.ID)
		if !strings.Contains(out, "resolved") {
			t.Errorf("expected 'resolved' in output: %s", out)
		}
		got := bdShow(t, bd, dir, gate.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("expected closed status after resolve, got %s", got.Status)
		}
	})

	t.Run("gate_resolve_with_reason", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Reason resolve")
		out := bdGate(t, bd, dir, "resolve", gate.ID, "--reason", "CI passed")
		if !strings.Contains(out, "resolved") {
			t.Errorf("expected 'resolved' in output: %s", out)
		}
		if !strings.Contains(out, "CI passed") {
			t.Logf("reason may not appear in text output: %s", out)
		}
		got := bdShow(t, bd, dir, gate.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("expected closed, got %s", got.Status)
		}
	})

	t.Run("gate_resolve_nonexistent", func(t *testing.T) {
		bdGateFail(t, bd, dir, "resolve", "tg-nonexistent999")
	})

	t.Run("gate_resolve_not_a_gate", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Not a gate resolve", "--type", "task")
		bdGateFail(t, bd, dir, "resolve", task.ID)
	})

	// ===== Gate Add-Waiter =====

	t.Run("gate_add_waiter", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Waiter gate")
		out := bdGate(t, bd, dir, "add-waiter", gate.ID, "agent-1")
		if !strings.Contains(out, "Added waiter") {
			t.Errorf("expected 'Added waiter' in output: %s", out)
		}
		// Verify waiter was added
		got := bdShow(t, bd, dir, gate.ID)
		if len(got.Waiters) != 1 || got.Waiters[0] != "agent-1" {
			t.Errorf("expected waiter [agent-1], got %v", got.Waiters)
		}
	})

	t.Run("gate_add_waiter_multiple", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Multi waiter gate")
		bdGate(t, bd, dir, "add-waiter", gate.ID, "agent-1")
		bdGate(t, bd, dir, "add-waiter", gate.ID, "agent-2")
		got := bdShow(t, bd, dir, gate.ID)
		if len(got.Waiters) != 2 {
			t.Errorf("expected 2 waiters, got %d: %v", len(got.Waiters), got.Waiters)
		}
	})

	t.Run("gate_add_waiter_duplicate", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Dup waiter gate")
		bdGate(t, bd, dir, "add-waiter", gate.ID, "agent-1")
		out := bdGate(t, bd, dir, "add-waiter", gate.ID, "agent-1")
		if !strings.Contains(out, "already registered") {
			t.Logf("duplicate waiter message: %s", out)
		}
		// Should still have only 1 waiter
		got := bdShow(t, bd, dir, gate.ID)
		if len(got.Waiters) != 1 {
			t.Errorf("expected 1 waiter after duplicate add, got %d", len(got.Waiters))
		}
	})

	t.Run("gate_add_waiter_nonexistent", func(t *testing.T) {
		bdGateFail(t, bd, dir, "add-waiter", "tg-nonexistent999", "agent-1")
	})

	// ===== Gate Check =====

	t.Run("gate_check_no_gates", func(t *testing.T) {
		// Create a fresh dir for this test to avoid interference
		checkDir, checkBeads, _ := bdInit(t, bd, "--prefix", "gc")
		cs := openStore(t, checkBeads, "gc")
		_ = cs.SetConfig(t.Context(), "types.custom", `["gate"]`)
		cs.Close()
		out := bdGate(t, bd, checkDir, "check")
		// Should not error even with no gates
		_ = out
	})

	t.Run("gate_check_dry_run", func(t *testing.T) {
		out := bdGate(t, bd, dir, "check", "--dry-run")
		// Dry-run should not close anything
		_ = out
	})

	t.Run("gate_check_with_type_filter", func(t *testing.T) {
		// Timer gates should be checkable
		out := bdGate(t, bd, dir, "check", "--type", "timer")
		_ = out
	})

	t.Run("gate_check_bead_type_retired", func(t *testing.T) {
		// beads-kburh retired the non-functional bead gate type end-to-end.
		// beads-68cgv then made `bd gate check --type` REJECT an unknown/retired
		// filter up front (validateGateCheckType, gate.go) rather than silently
		// matching zero gates + printing "No open gates" + exit 0 — the fail-late
		// footgun that reads as "all clear" while real gates go unchecked.
		//
		// beads-tvnvy: this test was landed by kburh (Jul 20) asserting the OLD
		// "runs clean / No open gates" no-op, but 68cgv landed AFTER and rejects
		// "bead" as an invalid filter → the test went RED on origin/main
		// (landed-test-vs-landed-feature conflict). 68cgv is the current, safe
		// design (retired type → fail-early reject, mirroring ds9tr on create),
		// so the test is reconciled to assert the REJECTION, not the stale no-op.
		target := bdCreate(t, bd, dir, "Bead gate target", "--type", "task")
		_ = createGate(t, bd, dir, "Bead gate",
			"--description", fmt.Sprintf("Waiting for %s", target.ID))

		// The retired "bead" filter must be rejected with a clear, actionable
		// error (68cgv), not silently accepted.
		out := bdGateFail(t, bd, dir, "check", "--type", "bead")
		if !strings.Contains(out, "invalid gate type filter") {
			t.Errorf("expected `bd gate check --type bead` to reject the retired filter with 'invalid gate type filter' (beads-68cgv/tvnvy), got: %s", out)
		}
	})

	t.Run("gate_check_limit", func(t *testing.T) {
		out := bdGate(t, bd, dir, "check", "--limit", "5")
		_ = out
	})

	// ===== Full Lifecycle =====

	t.Run("gate_lifecycle", func(t *testing.T) {
		// Create gate
		gate := createGate(t, bd, dir, "Lifecycle gate")

		// List shows it
		results := bdGateListJSON(t, bd, dir)
		found := false
		for _, r := range results {
			if r["id"] == gate.ID {
				found = true
			}
		}
		if !found {
			t.Error("expected new gate in list")
		}

		// Add waiter
		bdGate(t, bd, dir, "add-waiter", gate.ID, "lifecycle-agent")

		// Show gate with waiter
		got := bdShow(t, bd, dir, gate.ID)
		if len(got.Waiters) != 1 {
			t.Errorf("expected 1 waiter, got %d", len(got.Waiters))
		}

		// Resolve
		bdGate(t, bd, dir, "resolve", gate.ID, "--reason", "All done")

		// Verify closed
		got = bdShow(t, bd, dir, gate.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("expected closed after resolve, got %s", got.Status)
		}

		// Not in default list
		results = bdGateListJSON(t, bd, dir)
		for _, r := range results {
			if r["id"] == gate.ID {
				t.Error("resolved gate should not appear in default list")
			}
		}

		// In --all list
		results = bdGateListJSON(t, bd, dir, "--all")
		found = false
		for _, r := range results {
			if r["id"] == gate.ID {
				found = true
			}
		}
		if !found {
			t.Error("resolved gate should appear with --all")
		}
	})
}

// TestEmbeddedGateCreate exercises the "bd gate create" subcommand.
func TestEmbeddedGateCreate(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "gc")

	// Register "gate" as a custom type so bd gate create works.
	store := openStore(t, beadsDir, "gc")
	if err := store.SetConfig(t.Context(), "types.custom", `["gate"]`); err != nil {
		t.Fatalf("SetConfig types.custom: %v", err)
	}
	store.Close()

	t.Run("create_default_human_gate", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task for human gate", "--type", "task")

		out := bdGate(t, bd, dir, "create", "--blocks", task.ID)
		if !strings.Contains(out, "Created gate") {
			t.Errorf("expected 'Created gate' in output: %s", out)
		}
		if !strings.Contains(out, "type: human") {
			t.Errorf("expected default type 'human' in output: %s", out)
		}
		if !strings.Contains(out, task.ID) {
			t.Errorf("expected blocked issue ID in output: %s", out)
		}
	})

	t.Run("create_gate_json_output", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task for JSON gate", "--type", "task")

		cmd := exec.Command(bd, "gate", "create", "--blocks", task.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd gate create --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}

		var gate types.Issue
		s := strings.TrimSpace(stdout.String())
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON in output: %s", s)
		}
		if err := json.Unmarshal([]byte(s[start:]), &gate); err != nil {
			t.Fatalf("parse gate JSON: %v\n%s", err, s)
		}
		if gate.IssueType != types.IssueType("gate") {
			t.Errorf("expected issue_type=gate, got %s", gate.IssueType)
		}
		if gate.AwaitType != "human" {
			t.Errorf("expected await_type=human, got %s", gate.AwaitType)
		}
	})

	t.Run("create_gate_with_type_and_reason", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task for timer gate", "--type", "task")

		cmd := exec.Command(bd, "gate", "create", "--blocks", task.ID,
			"--type", "timer", "--timeout", "2h", "--reason", "Wait for cooldown", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd gate create --type=timer failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}

		var gate types.Issue
		s := strings.TrimSpace(stdout.String())
		start := strings.Index(s, "{")
		if err := json.Unmarshal([]byte(s[start:]), &gate); err != nil {
			t.Fatalf("parse gate JSON: %v\n%s", err, s)
		}
		if gate.AwaitType != "timer" {
			t.Errorf("expected await_type=timer, got %s", gate.AwaitType)
		}
		if gate.Timeout != 2*60*60*1e9 { // 2h in nanoseconds
			t.Errorf("expected timeout=2h, got %v", gate.Timeout)
		}
		if !strings.Contains(gate.Description, "Wait for cooldown") {
			t.Errorf("expected reason in description: %s", gate.Description)
		}
	})

	t.Run("create_gate_with_await_id", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task for PR gate", "--type", "task")

		cmd := exec.Command(bd, "gate", "create", "--blocks", task.ID,
			"--type", "gh:pr", "--await-id", "42", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd gate create --type=gh:pr failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}

		var gate types.Issue
		s := strings.TrimSpace(stdout.String())
		start := strings.Index(s, "{")
		if err := json.Unmarshal([]byte(s[start:]), &gate); err != nil {
			t.Fatalf("parse gate JSON: %v\n%s", err, s)
		}
		if gate.AwaitType != "gh:pr" {
			t.Errorf("expected await_type=gh:pr, got %s", gate.AwaitType)
		}
		if gate.AwaitID != "42" {
			t.Errorf("expected await_id=42, got %s", gate.AwaitID)
		}
		if gate.Title != "Gate: gh:pr 42" {
			t.Errorf("expected title 'Gate: gh:pr 42', got %s", gate.Title)
		}
	})

	t.Run("create_gate_blocks_ready", func(t *testing.T) {
		// Use a fresh db so ready output isn't polluted by other subtests
		freshDir, freshBeads, _ := bdInit(t, bd, "--prefix", "gr")
		fs := openStore(t, freshBeads, "gr")
		if err := fs.SetConfig(t.Context(), "types.custom", `["gate"]`); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		fs.Close()

		task := bdCreate(t, bd, freshDir, "Ready test task", "--type", "task")

		// Verify task appears in ready
		cmd := exec.Command(bd, "ready")
		cmd.Dir = freshDir
		cmd.Env = bdEnv(freshDir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "Ready test task") {
			t.Fatalf("task should appear in ready before gate: %s", stdout.String())
		}

		// Create gate blocking the task
		bdGate(t, bd, freshDir, "create", "--blocks", task.ID)

		// Verify task no longer in ready
		cmd = exec.Command(bd, "ready")
		cmd.Dir = freshDir
		cmd.Env = bdEnv(freshDir)
		stdout.Reset()
		stderr.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			// bd ready exits 0 even with no results
			t.Fatalf("bd ready failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if strings.Contains(stdout.String(), "Ready test task") {
			t.Errorf("task should NOT appear in ready while gate is open: %s", stdout.String())
		}
	})

	t.Run("create_gate_resolve_unblocks_ready", func(t *testing.T) {
		// Use a fresh db for clean ready output
		freshDir, freshBeads, _ := bdInit(t, bd, "--prefix", "gu")
		fs := openStore(t, freshBeads, "gu")
		if err := fs.SetConfig(t.Context(), "types.custom", `["gate"]`); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		fs.Close()

		task := bdCreate(t, bd, freshDir, "Unblock test task", "--type", "task")

		// Create and then resolve the gate
		gateOut := bdGate(t, bd, freshDir, "create", "--blocks", task.ID)

		// Extract gate ID from output ("Created gate gc-xxx ...")
		var gateID string
		for _, word := range strings.Fields(gateOut) {
			if strings.HasPrefix(word, "gu-") {
				gateID = word
				break
			}
		}
		if gateID == "" {
			t.Fatalf("could not extract gate ID from output: %s", gateOut)
		}

		// Resolve the gate
		bdGate(t, bd, freshDir, "resolve", gateID)

		// Verify task reappears in ready
		cmd := exec.Command(bd, "ready")
		cmd.Dir = freshDir
		cmd.Env = bdEnv(freshDir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "Unblock test task") {
			t.Errorf("task should reappear in ready after gate resolved: %s", stdout.String())
		}
	})

	t.Run("create_gate_missing_blocks_flag", func(t *testing.T) {
		out := bdGateFail(t, bd, dir, "create")
		if !strings.Contains(out, "blocks") {
			t.Errorf("expected error about missing --blocks flag: %s", out)
		}
	})

	t.Run("create_gate_nonexistent_target", func(t *testing.T) {
		out := bdGateFail(t, bd, dir, "create", "--blocks", "gc-nonexistent999")
		if !strings.Contains(out, "not found") {
			t.Errorf("expected 'not found' error: %s", out)
		}
	})

	t.Run("create_gate_bead_type_rejected", func(t *testing.T) {
		// beads-kburh: bead gates were retired (multi-rig routing removed) — a
		// bead gate could never resolve, so create must reject --type=bead
		// rather than mint a gate that strands its blocked issue forever.
		task := bdCreate(t, bd, dir, "Task for bead gate", "--type", "task")
		out := bdGateFail(t, bd, dir, "create", "--blocks", task.ID, "--type", "bead")
		if !strings.Contains(out, "invalid gate type") {
			t.Errorf("expected 'invalid gate type' error for --type=bead: %s", out)
		}
	})

	t.Run("create_gate_appears_in_gate_list", func(t *testing.T) {
		task := bdCreate(t, bd, dir, "Task for list check", "--type", "task")
		bdGate(t, bd, dir, "create", "--blocks", task.ID)

		results := bdGateListJSON(t, bd, dir)
		found := false
		for _, r := range results {
			if awaitType, ok := r["await_type"]; ok && awaitType == "human" {
				if desc, ok := r["description"].(string); ok && strings.Contains(desc, task.ID) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("expected gate blocking %s in gate list", task.ID)
		}
	})

	// ===== Gate check/resolve --json contract (beads-u3lt) =====

	// parseOneJSONDoc asserts stdout is EXACTLY one parseable JSON object with no
	// leading/trailing human text (the u3lt double-emit + empty-path failures
	// produced plaintext, or human-progress-text followed by a json doc).
	parseOneJSONDoc := func(t *testing.T, out, label string) map[string]interface{} {
		t.Helper()
		s := strings.TrimSpace(out)
		if s == "" {
			t.Fatalf("%s: stdout is EMPTY under --json (must be a JSON doc)", label)
		}
		if !strings.HasPrefix(s, "{") {
			t.Fatalf("%s: stdout has leading non-JSON text (double-emit / plaintext), got:\n%s", label, out)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			t.Fatalf("%s: stdout is not a single parseable JSON object: %v\n%s", label, err, out)
		}
		return obj
	}

	// gate check --json on the COMMON empty case must emit the summary JSON doc
	// (zero counts), not "No open gates found." plaintext + bare return (BUG 1).
	t.Run("gate_check_json_empty", func(t *testing.T) {
		// Fresh workspace so there are no open evaluable gates of a checkable type.
		edir, ebeads, _ := bdInit(t, bd, "--prefix", "ge")
		es := openStore(t, ebeads, "ge")
		_ = es.SetConfig(t.Context(), "types.custom", `["gate"]`)
		es.Close()
		out := bdGate(t, bd, edir, "check", "--json")
		obj := parseOneJSONDoc(t, out, "gate check --json (empty)")
		if obj["checked"] == nil {
			t.Errorf("expected 'checked' key in empty gate check --json summary: %v", obj)
		}
	})

	// gate resolve --json must emit a JSON success doc, not plaintext (resolve
	// honored --json NOWHERE before u3lt).
	t.Run("gate_resolve_json", func(t *testing.T) {
		gate := createGate(t, bd, dir, "Resolve JSON gate")
		out := bdGate(t, bd, dir, "resolve", gate.ID, "--json")
		obj := parseOneJSONDoc(t, out, "gate resolve --json")
		if obj["resolved"] != true {
			t.Errorf("expected resolved:true in gate resolve --json, got: %v", obj)
		}
		if obj["id"] != gate.ID {
			t.Errorf("expected id=%s in gate resolve --json, got: %v", gate.ID, obj["id"])
		}
	})

	// beads-jial: gate add-waiter direct-path guard errors must honor the
	// --json error contract, matching the sibling 'gate resolve' (u3lt) and
	// 'gate create'. Before the fix, `bd gate add-waiter <bad-id> --json` hit
	// a bare HandleError → stdout EMPTY + plaintext on stderr, breaking a
	// `2>&1 | jq` consumer. RED trigger is the same nonexistent-gate path as
	// gate_add_waiter_nonexistent above, now under --json.
	t.Run("gate_add_waiter_json_error_contract", func(t *testing.T) {
		fullArgs := []string{"gate", "add-waiter", "tg-nonexistent999", "agent-1", "--json"}
		cmd := exec.Command(bd, fullArgs...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("expected bd gate add-waiter <bad-id> --json to fail:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		}
		s := strings.TrimSpace(stdout.String())
		if s == "" {
			t.Fatalf("beads-jial: stdout is EMPTY under `gate add-waiter <bad-id> --json` "+
				"(bare HandleError instead of HandleErrorRespectJSON) — a --json error-contract "+
				"violation.\nstderr:\n%s", stderr.String())
		}
		obj := parseOneJSONDoc(t, s, "gate add-waiter --json (bad id)")
		if obj["error"] == nil {
			t.Errorf("beads-jial: expected an 'error' key in the --json error doc, got: %v", obj)
		}
	})

	// beads-w17gk: gate add-waiter's two SUCCESS legs (first-add + duplicate
	// idempotent no-op) must also honor --json. Before the fix both emitted a
	// bare fmt.Printf plaintext line on stdout regardless of --json — so a
	// consumer that json.loads(stdout) got "✓ Added waiter to gate ...", which
	// is unparseable, even though the ERROR legs (beads-jial) already emit JSON.
	// RED trigger: run the happy path under --json and assert stdout is a JSON
	// object carrying "added".
	t.Run("gate_add_waiter_json_success_contract", func(t *testing.T) {
		gate := createGate(t, bd, dir, "AddWaiter JSON success gate")

		// First add → {"gate":..., "waiter":..., "added":true}
		out := bdGate(t, bd, dir, "add-waiter", gate.ID, "json-agent", "--json")
		obj := parseOneJSONDoc(t, out, "gate add-waiter --json (first add)")
		if obj["added"] != true {
			t.Errorf("beads-w17gk: expected added:true on first add, got: %v", obj)
		}
		if obj["gate"] != gate.ID {
			t.Errorf("beads-w17gk: expected gate=%s in add-waiter --json, got: %v", gate.ID, obj["gate"])
		}
		if obj["waiter"] != "json-agent" {
			t.Errorf("beads-w17gk: expected waiter=json-agent in add-waiter --json, got: %v", obj["waiter"])
		}

		// Duplicate (idempotent no-op) → {"added":false}, NOT plaintext.
		out2 := bdGate(t, bd, dir, "add-waiter", gate.ID, "json-agent", "--json")
		obj2 := parseOneJSONDoc(t, out2, "gate add-waiter --json (duplicate no-op)")
		if obj2["added"] != false {
			t.Errorf("beads-w17gk: expected added:false on duplicate no-op, got: %v", obj2)
		}
	})
}

// TestEmbeddedGateConcurrent exercises gate operations concurrently.
func TestEmbeddedGateConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, gxBeads, _ := bdInit(t, bd, "--prefix", "gx")

	// Register "gate" as custom type.
	gxStore := openStore(t, gxBeads, "gx")
	if err := gxStore.SetConfig(t.Context(), "types.custom", `["gate"]`); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	gxStore.Close()

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

			// Each worker: create a gate, add a waiter, resolve it
			title := fmt.Sprintf("w%d-gate", worker)
			out, err := bdRunWithFlockRetry(t, bd, dir, "create", "--silent", title, "--type", "gate")
			if err != nil {
				r.err = fmt.Errorf("create gate: %v\n%s", err, out)
				results[worker] = r
				return
			}
			gateID := strings.TrimSpace(string(out))

			// Add waiter
			cmd := exec.Command(bd, "gate", "add-waiter", gateID, fmt.Sprintf("agent-%d", worker))
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err = cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("add-waiter %s: %v\n%s", gateID, err, out)
				results[worker] = r
				return
			}

			// Resolve
			cmd = exec.Command(bd, "gate", "resolve", gateID, "--reason", fmt.Sprintf("done-%d", worker))
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err = cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("resolve %s: %v\n%s", gateID, err, out)
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
