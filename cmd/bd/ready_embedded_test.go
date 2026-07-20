//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestEmbeddedReady(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rd")
	bdCreate(t, bd, dir, "Ready test issue", "--type", "task")

	// ===== Default =====

	t.Run("ready_includes_open_issue_with_zero_dependencies", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "GH3268 zero dependency ready issue", "--type", "task", "--label", "gh3268-zero-deps")

		cmd := exec.Command(bd, "ready", "--json", "--label", "gh3268-zero-deps")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}

		var ready []types.IssueWithCounts
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ready); err != nil {
			t.Fatalf("parse ready JSON: %v\n%s", err, stdout.String())
		}
		if len(ready) != 1 {
			t.Fatalf("ready count = %d, want 1: %s", len(ready), stdout.String())
		}
		if ready[0].ID != issue.ID {
			t.Fatalf("ready ID = %s, want %s", ready[0].ID, issue.ID)
		}
		if ready[0].DependencyCount != 0 {
			t.Fatalf("dependency_count = %d, want 0", ready[0].DependencyCount)
		}
	})

	t.Run("ready_default", func(t *testing.T) {
		cmd := exec.Command(bd, "ready")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "Ready test issue") {
			t.Errorf("expected issue in ready output: %s", stdout.String())
		}
	})

	// ===== --json =====

	t.Run("ready_json", func(t *testing.T) {
		cmd := exec.Command(bd, "ready", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		s := strings.TrimSpace(stdout.String())
		start := strings.IndexAny(s, "[{")
		if start < 0 {
			t.Fatalf("no JSON in ready --json output: %s", s)
		}
		if !json.Valid([]byte(s[start:])) {
			t.Errorf("invalid JSON in ready output: %s", s[:min(200, len(s))])
		}
	})

	t.Run("ready_json_truncation_hint", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			bdCreate(t, bd, dir, fmt.Sprintf("Ready capped issue %d", i), "--type", "task")
		}

		cmd := exec.Command(bd, "ready", "--json", "--limit", "2")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("bd ready --json --limit 2 failed: %v\nstderr: %s\nstdout: %s", err, stderr.String(), out)
		}
		if !json.Valid(bytes.TrimSpace(out)) {
			t.Fatalf("ready JSON stdout should remain parseable, got: %s", out)
		}
		if !strings.Contains(stderr.String(), "Use --limit 0 for all") {
			t.Fatalf("expected truncation hint on stderr, got: %q", stderr.String())
		}
	})

	t.Run("ready_claim_json", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Ready claim json", "--type", "task", "--label", "ready-claim-json")

		cmd := exec.Command(bd, "ready", "--claim", "--json", "--label", "missing-label")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready --claim --json with no matches failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		var empty []types.IssueWithCounts
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &empty); err != nil {
			t.Fatalf("parse empty claim JSON: %v\n%s", err, stdout.String())
		}
		if len(empty) != 0 {
			t.Fatalf("expected no claimed issues for unmatched label, got %d", len(empty))
		}

		cmd = exec.Command(bd, "ready", "--claim", "--json", "--label", "ready-claim-json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout.Reset()
		stderr.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("bd ready --claim --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		var claimed []types.IssueWithCounts
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &claimed); err != nil {
			t.Fatalf("parse claim JSON: %v\n%s", err, stdout.String())
		}
		if len(claimed) != 1 {
			t.Fatalf("expected one claimed issue, got %d: %s", len(claimed), stdout.String())
		}
		if claimed[0].ID != issue.ID {
			t.Fatalf("claimed ID = %s, want %s", claimed[0].ID, issue.ID)
		}
		if claimed[0].Status != types.StatusInProgress {
			t.Fatalf("claimed status = %s, want %s", claimed[0].Status, types.StatusInProgress)
		}
		if claimed[0].Assignee == "" {
			t.Fatal("expected claimed issue to have assignee")
		}
	})

	// ===== With Blockers =====

	t.Run("ready_excludes_blocked", func(t *testing.T) {
		blocker := bdCreate(t, bd, dir, "Blocker issue", "--type", "task")
		blocked := bdCreate(t, bd, dir, "Blocked by blocker", "--type", "task")

		// Add blocking dependency: blocked depends on blocker
		cmd := exec.Command(bd, "dep", "add", blocked.ID, blocker.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add failed: %v\n%s", err, out)
		}

		cmd = exec.Command(bd, "ready")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		// The blocked issue should not appear in ready output
		if strings.Contains(stdout.String(), "Blocked by blocker") {
			t.Errorf("blocked issue should not appear in ready output: %s", stdout.String())
		}
	})

	// ===== Exclude Label =====

	t.Run("ready_exclude_label", func(t *testing.T) {
		bdCreate(t, bd, dir, "Triage pending item", "--type", "task", "--label", "triage:pending")
		bdCreate(t, bd, dir, "Normal ready item", "--type", "task")

		cmd := exec.Command(bd, "ready", "--exclude-label", "triage:pending")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready --exclude-label failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if strings.Contains(stdout.String(), "Triage pending item") {
			t.Errorf("triage:pending issue should not appear with --exclude-label: %s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "Normal ready item") {
			t.Errorf("normal issue should still appear with --exclude-label: %s", stdout.String())
		}
	})

	// ===== -C flag =====

	t.Run("ready_with_C_flag", func(t *testing.T) {
		// Run bd ready from a different directory using -C to point at the beads project
		tmpDir := t.TempDir()
		cmd := exec.Command(bd, "-C", dir, "ready")
		cmd.Dir = tmpDir // Run from a directory with no .beads/
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd -C %s ready failed: %v\nstdout:\n%s\nstderr:\n%s", dir, err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "Ready test issue") {
			t.Errorf("expected issue in ready -C output: %s", stdout.String())
		}
	})

	t.Run("ready_with_C_flag_invalid_path", func(t *testing.T) {
		tmpDir := t.TempDir()
		cmd := exec.Command(bd, "-C", filepath.Join(tmpDir, "missing"), "ready")
		cmd.Dir = tmpDir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("bd -C missing ready succeeded unexpectedly:\n%s", out)
		}
		if !strings.Contains(string(out), "cannot use -C directory") {
			t.Errorf("expected invalid -C path error, got: %s", out)
		}
	})

	t.Run("ready_with_C_flag_does_not_leak_cwd", func(t *testing.T) {
		// Verify that -C does not permanently mutate the process cwd.
		// Two sequential invocations from the same tmpDir: the first uses -C to
		// reach the project; the second omits -C and must fail (no .beads/ in tmpDir),
		// proving BEADS_DIR was not leaked into the test process environment.
		tmpDir := t.TempDir()
		env := bdEnv(dir) // strips all BEADS_* vars

		cmd1 := exec.Command(bd, "-C", dir, "ready")
		cmd1.Dir = tmpDir
		cmd1.Env = env
		if out, err := cmd1.CombinedOutput(); err != nil {
			t.Fatalf("first bd -C ready failed: %v\n%s", err, out)
		}

		cmd2 := exec.Command(bd, "ready")
		cmd2.Dir = tmpDir
		cmd2.Env = env // same env — BEADS_DIR must not have leaked
		out2, err2 := cmd2.CombinedOutput()
		if err2 == nil {
			t.Fatalf("second bd ready (no -C) should have failed in tmpDir, got: %s", out2)
		}
	})

	t.Run("offset_rejected_outside_proxied", func(t *testing.T) {
		cmd := exec.Command(bd, "ready", "--offset", "1")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("bd ready --offset 1 in embedded mode should have failed, got: %s", out)
		}
		if !strings.Contains(string(out), "--offset is only supported under --proxied-server") {
			t.Errorf("expected '--offset is only supported under --proxied-server' error, got: %s", out)
		}
	})

	// beads-57tt: the DIRECT (non-proxied) RunE guards --priority via
	// ValidatePriority (StringP flag). An out-of-range value used to be accepted
	// silently (IntP + GetInt → matched nothing, exit 0). Also confirms the
	// P0-P4 form now parses (IntP could not).
	t.Run("priority_validation_direct", func(t *testing.T) {
		cmd := exec.Command(bd, "ready", "--priority", "99")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("bd ready --priority 99 should have failed, got: %s", out)
		}
		if !strings.Contains(string(out), "invalid priority") {
			t.Errorf("expected 'invalid priority' error, got: %s", out)
		}
		// P-prefixed and plain numeric forms both succeed (no false reject).
		for _, val := range []string{"P2", "2"} {
			c := exec.Command(bd, "ready", "--priority", val)
			c.Dir = dir
			c.Env = bdEnv(dir)
			if o, err := c.CombinedOutput(); err != nil {
				t.Errorf("bd ready --priority %s should succeed, got err: %v\n%s", val, err, o)
			}
		}
	})
}

// TestEmbeddedReadyPriorityRange is the teeth for beads-cseh3: bd ready must
// support --priority-min/--priority-max (a P0-P4 range) with the same semantics
// as bd list, not just exact --priority. Previously ready registered only
// --priority; the range flags were unknown and the sqlbuild WHERE builder
// ignored filter.PriorityMin/Max even though the struct carried them.
func TestEmbeddedReadyPriorityRange(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rp")

	// One unblocked issue at each priority P0..P4.
	byPrio := map[int]string{}
	for p := 0; p <= 4; p++ {
		iss := bdCreate(t, bd, dir, fmt.Sprintf("prio-%d", p), "--type", "task", "--priority", fmt.Sprintf("%d", p))
		byPrio[p] = iss.ID
	}

	// readyPriorities runs `bd ready --json <args>` and returns the set of
	// priorities present in the result.
	readyPriorities := func(t *testing.T, args ...string) map[int]bool {
		t.Helper()
		full := append([]string{"ready", "--json", "--limit", "0"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		var ready []types.IssueWithCounts
		if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ready); jerr != nil {
			t.Fatalf("parse ready JSON (%v): %v\n%s", args, jerr, stdout.String())
		}
		got := map[int]bool{}
		for _, r := range ready {
			got[r.Priority] = true
		}
		return got
	}

	t.Run("priority_max_1_returns_only_P0_P1", func(t *testing.T) {
		got := readyPriorities(t, "--priority-max", "1")
		for p := 0; p <= 4; p++ {
			want := p <= 1
			if got[p] != want {
				t.Errorf("--priority-max 1: P%d present=%v, want %v (got set %v)", p, got[p], want, got)
			}
		}
	})

	t.Run("priority_min_3_returns_only_P3_P4", func(t *testing.T) {
		got := readyPriorities(t, "--priority-min", "3")
		for p := 0; p <= 4; p++ {
			want := p >= 3
			if got[p] != want {
				t.Errorf("--priority-min 3: P%d present=%v, want %v (got set %v)", p, got[p], want, got)
			}
		}
	})

	t.Run("priority_range_min2_max3_returns_only_P2_P3", func(t *testing.T) {
		got := readyPriorities(t, "--priority-min", "2", "--priority-max", "3")
		for p := 0; p <= 4; p++ {
			want := p == 2 || p == 3
			if got[p] != want {
				t.Errorf("--priority-min 2 --priority-max 3: P%d present=%v, want %v (got set %v)", p, got[p], want, got)
			}
		}
	})

	t.Run("priority_min_P0_form_accepted_and_keeps_P0", func(t *testing.T) {
		// P0 (=0) must not be dropped by the Changed() guard, and the P-prefixed
		// form must parse (mirrors the exact --priority beads-57tt handling).
		got := readyPriorities(t, "--priority-max", "P0")
		if !got[0] {
			t.Errorf("--priority-max P0 should include the P0 issue, got set %v", got)
		}
		for p := 1; p <= 4; p++ {
			if got[p] {
				t.Errorf("--priority-max P0: P%d must be excluded, got set %v", p, got)
			}
		}
	})

	t.Run("out_of_range_priority_min_rejected", func(t *testing.T) {
		cmd := exec.Command(bd, "ready", "--priority-min", "99")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("bd ready --priority-min 99 should have failed, got: %s", out)
		}
		if !strings.Contains(string(out), "invalid priority") {
			t.Errorf("expected 'invalid priority' error, got: %s", out)
		}
	})
}

// TestEmbeddedReadyDescContains is the teeth for beads-6na9a: bd ready must
// support --desc-contains (case-insensitive substring on description) with the
// same semantics as bd list. Uses distinct titles + shared description
// substring to prove it matches on description, not title.
func TestEmbeddedReadyDescContains(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dc")

	// Titles deliberately do NOT contain "kafka"; only the descriptions do,
	// so a match proves description-substring filtering (not title).
	bdCreate(t, bd, dir, "Alpha task", "--type", "task", "-d", "Investigate the KAFKA consumer lag")
	bdCreate(t, bd, dir, "Beta task", "--type", "task", "-d", "Tune kafka partition rebalancing")
	bdCreate(t, bd, dir, "Gamma task", "--type", "task", "-d", "Refresh the sprocket dashboard")

	readyTitles := func(t *testing.T, args ...string) []string {
		t.Helper()
		full := append([]string{"ready", "--json", "--limit", "0"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		var ready []types.IssueWithCounts
		if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ready); jerr != nil {
			t.Fatalf("parse ready JSON (%v): %v\n%s", args, jerr, stdout.String())
		}
		titles := make([]string, 0, len(ready))
		for _, r := range ready {
			titles = append(titles, r.Title)
		}
		return titles
	}

	t.Run("case_insensitive_description_substring_matches_both", func(t *testing.T) {
		got := readyTitles(t, "--desc-contains", "kafka")
		if len(got) != 2 {
			t.Fatalf("--desc-contains kafka: got %d titles, want 2: %v", len(got), got)
		}
	})

	t.Run("nonmatching_substring_returns_empty", func(t *testing.T) {
		got := readyTitles(t, "--desc-contains", "nonesuch")
		if len(got) != 0 {
			t.Errorf("--desc-contains nonesuch: expected no matches, got %v", got)
		}
	})

	t.Run("distinct_substring_matches_one", func(t *testing.T) {
		got := readyTitles(t, "--desc-contains", "sprocket")
		if len(got) != 1 || got[0] != "Gamma task" {
			t.Errorf("--desc-contains sprocket: got %v, want [Gamma task]", got)
		}
	})
}

// TestEmbeddedReadyNotesContains is the teeth for beads-j95lq: bd ready must
// support --notes-contains (case-insensitive substring on notes) with the same
// semantics as bd list. Previously ready had no such flag and the WorkFilter/
// sqlbuild builder ignored notes entirely.
func TestEmbeddedReadyNotesContains(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "nc")

	// Titles/descriptions deliberately do NOT contain "kafka"; only the notes
	// do, so a match proves notes-substring filtering (not title/description).
	bdCreate(t, bd, dir, "Alpha task", "--type", "task", "-d", "generic desc", "--notes", "Investigate the KAFKA consumer lag")
	bdCreate(t, bd, dir, "Beta task", "--type", "task", "-d", "generic desc", "--notes", "Tune kafka partition rebalancing")
	bdCreate(t, bd, dir, "Gamma task", "--type", "task", "-d", "generic desc", "--notes", "Refresh the sprocket dashboard")

	readyTitles := func(t *testing.T, args ...string) []string {
		t.Helper()
		full := append([]string{"ready", "--json", "--limit", "0"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		var ready []types.IssueWithCounts
		if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ready); jerr != nil {
			t.Fatalf("parse ready JSON (%v): %v\n%s", args, jerr, stdout.String())
		}
		titles := make([]string, 0, len(ready))
		for _, r := range ready {
			titles = append(titles, r.Title)
		}
		return titles
	}

	t.Run("case_insensitive_notes_substring_matches_both", func(t *testing.T) {
		got := readyTitles(t, "--notes-contains", "kafka")
		if len(got) != 2 {
			t.Fatalf("--notes-contains kafka: got %d titles, want 2: %v", len(got), got)
		}
	})

	t.Run("nonmatching_substring_returns_empty", func(t *testing.T) {
		got := readyTitles(t, "--notes-contains", "nonesuch")
		if len(got) != 0 {
			t.Errorf("--notes-contains nonesuch: expected no matches, got %v", got)
		}
	})

	t.Run("distinct_substring_matches_one", func(t *testing.T) {
		got := readyTitles(t, "--notes-contains", "sprocket")
		if len(got) != 1 || got[0] != "Gamma task" {
			t.Errorf("--notes-contains sprocket: got %v, want [Gamma task]", got)
		}
	})
}

// TestEmbeddedReadyTitleContains is the teeth for beads-d1as8: bd ready must
// support --title-contains (case-insensitive substring on title) with the same
// semantics as bd list. Previously ready had no such flag and the WorkFilter/
// sqlbuild builder ignored the title entirely.
func TestEmbeddedReadyTitleContains(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tc")

	bdCreate(t, bd, dir, "Fix the widget renderer", "--type", "task")
	bdCreate(t, bd, dir, "Refactor WIDGET storage layer", "--type", "task")
	bdCreate(t, bd, dir, "Document the sprocket API", "--type", "task")

	readyTitles := func(t *testing.T, args ...string) []string {
		t.Helper()
		full := append([]string{"ready", "--json", "--limit", "0"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		var ready []types.IssueWithCounts
		if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ready); jerr != nil {
			t.Fatalf("parse ready JSON (%v): %v\n%s", args, jerr, stdout.String())
		}
		titles := make([]string, 0, len(ready))
		for _, r := range ready {
			titles = append(titles, r.Title)
		}
		return titles
	}

	t.Run("case_insensitive_substring_matches_both_widget_issues", func(t *testing.T) {
		got := readyTitles(t, "--title-contains", "widget")
		if len(got) != 2 {
			t.Fatalf("--title-contains widget: got %d titles, want 2: %v", len(got), got)
		}
		for _, tt := range got {
			if !strings.Contains(strings.ToLower(tt), "widget") {
				t.Errorf("unexpected title %q for --title-contains widget", tt)
			}
		}
	})

	t.Run("nonmatching_substring_returns_empty", func(t *testing.T) {
		got := readyTitles(t, "--title-contains", "nonesuch")
		if len(got) != 0 {
			t.Errorf("--title-contains nonesuch: expected no matches, got %v", got)
		}
	})

	t.Run("distinct_substring_matches_one", func(t *testing.T) {
		got := readyTitles(t, "--title-contains", "sprocket")
		if len(got) != 1 || !strings.Contains(strings.ToLower(got[0]), "sprocket") {
			t.Errorf("--title-contains sprocket: got %v, want the sprocket issue only", got)
		}
	})
}

// TestEmbeddedReadyTitleContainsEscapesLikeMetachars is the teeth for
// beads-b9ova: --title-contains must treat % and _ as LITERAL characters, not
// LIKE wildcards. Runs against real Dolt so the ESCAPE '\\' clause + backslash
// escaping are exercised end-to-end (a pure clause-string test would false-green).
func TestEmbeddedReadyTitleContainsEscapesLikeMetachars(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "lm")

	// One title literally contains '%'; the others do not. A bare '%' filter
	// must match ONLY the literal-'%' issue, not all rows (the b9ova bug).
	bdCreate(t, bd, dir, "reach 100% coverage", "--type", "task")
	bdCreate(t, bd, dir, "plain alpha task", "--type", "task")
	bdCreate(t, bd, dir, "plain beta task", "--type", "task")

	readyTitles := func(t *testing.T, args ...string) []string {
		t.Helper()
		full := append([]string{"ready", "--json", "--limit", "0"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		var ready []types.IssueWithCounts
		if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ready); jerr != nil {
			t.Fatalf("parse ready JSON (%v): %v\n%s", args, jerr, stdout.String())
		}
		titles := make([]string, 0, len(ready))
		for _, r := range ready {
			titles = append(titles, r.Title)
		}
		return titles
	}

	t.Run("literal_percent_matches_only_the_percent_issue", func(t *testing.T) {
		got := readyTitles(t, "--title-contains", "%")
		if len(got) != 1 {
			t.Fatalf("b9ova: --title-contains '%%' should match ONLY the literal-'%%' issue, got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0], "100%") {
			t.Errorf("expected the '100%%' issue, got %q", got[0])
		}
	})

	t.Run("literal_underscore_matches_none", func(t *testing.T) {
		// No title contains '_'; a bare '_' must match NONE (not act as
		// single-char wildcard matching every row).
		got := readyTitles(t, "--title-contains", "_")
		if len(got) != 0 {
			t.Errorf("b9ova: --title-contains '_' should match none (no literal underscore), got %v", got)
		}
	})
}

// TestEmbeddedReadyDateRange is the teeth for beads-10y4y: bd ready must support
// --created-after/--created-before/--updated-after/--updated-before date-range
// filters with the same relative-time-aware semantics as bd list. Previously
// ready had none of these flags and WorkFilter/the sqlbuild builder ignored
// created_at/updated_at entirely.
func TestEmbeddedReadyDateRange(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dr")

	// One freshly-created, unblocked issue (created_at/updated_at = ~now).
	bdCreate(t, bd, dir, "date range subject", "--type", "task")

	readyCount := func(t *testing.T, args ...string) int {
		t.Helper()
		full := append([]string{"ready", "--json", "--limit", "0"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		var ready []types.IssueWithCounts
		if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &ready); jerr != nil {
			t.Fatalf("parse ready JSON (%v): %v\n%s", args, jerr, stdout.String())
		}
		return len(ready)
	}

	t.Run("created_after_yesterday_includes_fresh_issue", func(t *testing.T) {
		if n := readyCount(t, "--created-after", "yesterday"); n != 1 {
			t.Errorf("--created-after yesterday: got %d, want 1", n)
		}
	})
	t.Run("created_after_tomorrow_excludes_fresh_issue", func(t *testing.T) {
		if n := readyCount(t, "--created-after", "tomorrow"); n != 0 {
			t.Errorf("--created-after tomorrow: got %d, want 0", n)
		}
	})
	t.Run("created_before_tomorrow_includes_fresh_issue", func(t *testing.T) {
		if n := readyCount(t, "--created-before", "tomorrow"); n != 1 {
			t.Errorf("--created-before tomorrow: got %d, want 1", n)
		}
	})
	t.Run("created_before_yesterday_excludes_fresh_issue", func(t *testing.T) {
		if n := readyCount(t, "--created-before", "yesterday"); n != 0 {
			t.Errorf("--created-before yesterday: got %d, want 0", n)
		}
	})
	t.Run("updated_after_yesterday_includes_fresh_issue", func(t *testing.T) {
		if n := readyCount(t, "--updated-after", "yesterday"); n != 1 {
			t.Errorf("--updated-after yesterday: got %d, want 1", n)
		}
	})
	t.Run("updated_before_yesterday_excludes_fresh_issue", func(t *testing.T) {
		if n := readyCount(t, "--updated-before", "yesterday"); n != 0 {
			t.Errorf("--updated-before yesterday: got %d, want 0", n)
		}
	})

	t.Run("invalid_date_rejected", func(t *testing.T) {
		cmd := exec.Command(bd, "ready", "--created-after", "not-a-date")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("bd ready --created-after not-a-date should have failed, got: %s", out)
		}
		if !strings.Contains(string(out), "created-after") {
			t.Errorf("expected a --created-after parse error, got: %s", out)
		}
	})
}

func TestEmbeddedReadyConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rx")

	bdCreate(t, bd, dir, "Ready concurrent issue", "--type", "task")

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
			cmd := exec.Command(bd, "ready")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("ready (worker %d): %v\n%s", worker, err, out)
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

func TestEmbeddedReadyClaimConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rc")

	issue := bdCreate(t, bd, dir, "Ready claim concurrent issue", "--type", "task")

	const numWorkers = 8
	type workerResult struct {
		worker  int
		claimed []types.IssueWithCounts
		err     error
		out     string
	}
	results := make([]workerResult, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			r := workerResult{worker: worker}
			out, err := bdRunWithFlockRetry(t, bd, dir, "ready", "--claim", "--json")
			r.out = string(out)
			if err != nil {
				r.err = fmt.Errorf("ready --claim (worker %d): %v\n%s", worker, err, out)
				results[worker] = r
				return
			}
			if err := json.Unmarshal(bytes.TrimSpace(out), &r.claimed); err != nil {
				r.err = fmt.Errorf("parse ready --claim JSON (worker %d): %v\n%s", worker, err, out)
			}
			results[worker] = r
		}(w)
	}
	wg.Wait()

	claimCount := 0
	for _, r := range results {
		if r.err != nil {
			t.Errorf("worker %d failed: %v", r.worker, r.err)
			continue
		}
		if len(r.claimed) > 1 {
			t.Errorf("worker %d claimed %d issues: %s", r.worker, len(r.claimed), r.out)
			continue
		}
		if len(r.claimed) == 1 {
			claimCount++
			if r.claimed[0].ID != issue.ID {
				t.Errorf("worker %d claimed %s, want %s", r.worker, r.claimed[0].ID, issue.ID)
			}
		}
	}
	if claimCount != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", claimCount)
	}
	got := bdShow(t, bd, dir, issue.ID)
	if got.Status != types.StatusInProgress {
		t.Fatalf("final status = %s, want %s", got.Status, types.StatusInProgress)
	}
	if got.Assignee == "" {
		t.Fatal("expected final assignee to be set")
	}
}

// TestEmbeddedReadyParentExistenceCheck verifies bd ready --parent <nonexistent>
// errors (exit != 0) in both text and --json, rather than silently returning []
// exit 0 ("No ready work found"). Mirrors TestEmbeddedBlockedParentExistenceCheck
// (beads-d5jg) — the plain-ready default path was missed when the blocked path
// was fixed (beads-e875).
func TestEmbeddedReadyParentExistenceCheck(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rpe")
	epic := bdCreate(t, bd, dir, "real epic", "--type", "epic")

	// Nonexistent parent must error, in both text and --json.
	for _, args := range [][]string{
		{"ready", "--parent", "rpe-nonexistent"},
		{"ready", "--parent", "rpe-nonexistent", "--json"},
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

	// A real, childless epic must NOT error — a valid query with an empty result
	// (surgical: the guard only rejects missing parents).
	cmd := exec.Command(bd, "ready", "--parent", epic.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd ready --parent %s (valid childless): expected success, got %v:\n%s", epic.ID, err, out)
	}
}
