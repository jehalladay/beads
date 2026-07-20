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

// bdCount runs "bd count" with the given args and returns raw stdout.
// Stderr (warnings, tips) is captured separately so it does not pollute
// callers that parse stdout.
func bdCount(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"count"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd count %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdCountFail runs "bd count" expecting failure.
func bdCountFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"count"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd count %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdCountJSON runs "bd count --json" and parses the result.
// Stderr is captured separately so warnings do not corrupt JSON parsing.
func bdCountJSON(t *testing.T, bd, dir string, args ...string) map[string]interface{} {
	t.Helper()
	fullArgs := append([]string{"count", "--json"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd count --json %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	s := strings.TrimSpace(stdout.String())
	start := strings.IndexAny(s, "{")
	if start < 0 {
		t.Fatalf("no JSON object in count output: %s\nstderr:\n%s", s, stderr.String())
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s[start:]), &m); err != nil {
		t.Fatalf("parse count JSON: %v\n%s\nstderr:\n%s", err, s, stderr.String())
	}
	return m
}

func TestEmbeddedCount(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ct")

	// Pre-create a varied set of issues for filter testing.
	bdCreate(t, bd, dir, "Count bug one", "--type", "bug", "--priority", "1", "--assignee", "alice")
	bdCreate(t, bd, dir, "Count bug two", "--type", "bug", "--priority", "2", "--assignee", "bob", "--description", "has a description")
	bdCreate(t, bd, dir, "Count task one", "--type", "task", "--priority", "3", "--assignee", "alice")
	bdCreate(t, bd, dir, "Count feature one", "--type", "feature", "--priority", "1")
	closedIssue := bdCreate(t, bd, dir, "Count closed one", "--type", "task", "--priority", "2", "--assignee", "alice")
	bdClose(t, bd, dir, closedIssue.ID)
	bdCreate(t, bd, dir, "Count labeled", "--type", "task", "--label", "frontend", "--label", "urgent")
	bdCreate(t, bd, dir, "Count labeled two", "--type", "task", "--label", "backend")
	bdCreate(t, bd, dir, "Count notes issue", "--type", "task", "--description", "notes keyword here")

	// ===== Basic count =====

	t.Run("basic_count_no_filters", func(t *testing.T) {
		out := strings.TrimSpace(bdCount(t, bd, dir))
		// Should return a number >= 8 (we created 8 issues)
		if out == "0" {
			t.Error("expected non-zero count")
		}
	})

	// ===== Status filter =====

	t.Run("filter_by_status_open", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--status", "open")
		count := int(m["count"].(float64))
		if count < 7 {
			t.Errorf("expected at least 7 open issues, got %d", count)
		}
	})

	t.Run("filter_by_status_closed", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--status", "closed")
		count := int(m["count"].(float64))
		if count < 1 {
			t.Errorf("expected at least 1 closed issue, got %d", count)
		}
	})

	// ===== Priority filter =====

	t.Run("filter_by_priority", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--priority", "1")
		count := int(m["count"].(float64))
		if count < 2 {
			t.Errorf("expected at least 2 priority-1 issues, got %d", count)
		}
	})

	// ===== Assignee filter =====

	t.Run("filter_by_assignee", func(t *testing.T) {
		// beads-khn67: use --status all so this asserts the ASSIGNEE filter
		// independent of the beads-9iia default closed/pinned exclusion (which
		// otherwise hides the closed alice issue, making the count scope-dependent).
		m := bdCountJSON(t, bd, dir, "--assignee", "alice", "--status", "all")
		count := int(m["count"].(float64))
		if count < 3 {
			t.Errorf("expected at least 3 issues assigned to alice, got %d", count)
		}
	})

	// ===== Type filter =====

	t.Run("filter_by_type", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--type", "bug")
		count := int(m["count"].(float64))
		if count < 2 {
			t.Errorf("expected at least 2 bugs, got %d", count)
		}
	})

	// ===== Label filter (AND) =====

	t.Run("filter_by_label_and", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--label", "frontend", "--label", "urgent")
		count := int(m["count"].(float64))
		if count < 1 {
			t.Errorf("expected at least 1 issue with both labels, got %d", count)
		}
	})

	// ===== Label filter (OR) =====

	t.Run("filter_by_label_any", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--label-any", "frontend", "--label-any", "backend")
		count := int(m["count"].(float64))
		if count < 2 {
			t.Errorf("expected at least 2 issues with either label, got %d", count)
		}
	})

	// ===== Title filter =====

	t.Run("filter_by_title", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--title", "bug")
		count := int(m["count"].(float64))
		if count >= 2 {
			// "Count bug one" and "Count bug two" contain "bug"
		} else {
			t.Errorf("expected at least 2 issues matching title 'bug', got %d", count)
		}
	})

	// ===== Title-contains =====

	t.Run("filter_by_title_contains", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--title-contains", "feature")
		count := int(m["count"].(float64))
		if count < 1 {
			t.Errorf("expected at least 1 issue with 'feature' in title, got %d", count)
		}
	})

	// ===== Desc-contains =====

	t.Run("filter_by_desc_contains", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--desc-contains", "notes keyword")
		count := int(m["count"].(float64))
		if count < 1 {
			t.Errorf("expected at least 1 issue with 'notes keyword' in description, got %d", count)
		}
	})

	// ===== Date range filters =====

	t.Run("filter_by_created_after", func(t *testing.T) {
		// All issues were just created, so created-after yesterday should match all.
		// beads-khn67: --status all so the created-date filter is tested independent
		// of the beads-9iia default closed/pinned exclusion (which hides the closed issue).
		m := bdCountJSON(t, bd, dir, "--created-after", "2000-01-01", "--status", "all")
		count := int(m["count"].(float64))
		if count < 8 {
			t.Errorf("expected at least 8 issues created after 2000-01-01, got %d", count)
		}
	})

	t.Run("filter_by_created_before", func(t *testing.T) {
		// created-before a past date should return 0
		m := bdCountJSON(t, bd, dir, "--created-before", "2000-01-01")
		count := int(m["count"].(float64))
		if count != 0 {
			t.Errorf("expected 0 issues created before 2000-01-01, got %d", count)
		}
	})

	t.Run("filter_by_updated_after", func(t *testing.T) {
		// beads-khn67: --status all so the updated-date filter is tested independent
		// of the beads-9iia default closed/pinned exclusion.
		m := bdCountJSON(t, bd, dir, "--updated-after", "2000-01-01", "--status", "all")
		count := int(m["count"].(float64))
		if count < 8 {
			t.Errorf("expected at least 8 issues updated after 2000-01-01, got %d", count)
		}
	})

	t.Run("filter_by_closed_after", func(t *testing.T) {
		// beads-khn67 regression: --closed-after must NOT need --status all. A
		// --closed-* filter only matches closed issues (closed_at is NULL until
		// close), so the beads-9iia default closed/pinned exclusion must be
		// skipped when a --closed-* flag is present — otherwise this returns 0.
		m := bdCountJSON(t, bd, dir, "--closed-after", "2000-01-01")
		count := int(m["count"].(float64))
		if count < 1 {
			t.Errorf("expected at least 1 closed issue after 2000-01-01, got %d (beads-khn67: default-exclude must not hide closed issues from --closed-after)", count)
		}
	})

	// ===== Empty description filter =====

	t.Run("filter_empty_description", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--empty-description")
		count := int(m["count"].(float64))
		// Several issues were created without --description
		if count < 1 {
			t.Errorf("expected at least 1 issue with empty description, got %d", count)
		}
	})

	// ===== No assignee filter =====

	t.Run("filter_no_assignee", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--no-assignee")
		count := int(m["count"].(float64))
		if count < 1 {
			t.Errorf("expected at least 1 issue with no assignee, got %d", count)
		}
	})

	// ===== No labels filter =====

	t.Run("filter_no_labels", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--no-labels")
		count := int(m["count"].(float64))
		if count < 1 {
			t.Errorf("expected at least 1 issue with no labels, got %d", count)
		}
	})

	// ===== Priority range filter =====

	t.Run("filter_priority_min_max", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--priority-min", "1", "--priority-max", "2")
		count := int(m["count"].(float64))
		if count < 3 {
			t.Errorf("expected at least 3 issues with priority 1-2, got %d", count)
		}
	})

	// ===== Group by status =====

	t.Run("group_by_status", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--by-status")
		total := int(m["total"].(float64))
		if total < 8 {
			t.Errorf("expected total >= 8, got %d", total)
		}
		groups, ok := m["groups"].([]interface{})
		if !ok || len(groups) == 0 {
			t.Fatal("expected groups array")
		}
		// Should have at least "open" and "closed" groups
		foundOpen := false
		foundClosed := false
		for _, g := range groups {
			gm := g.(map[string]interface{})
			if gm["group"] == "open" {
				foundOpen = true
			}
			if gm["group"] == "closed" {
				foundClosed = true
			}
		}
		if !foundOpen {
			t.Error("expected 'open' group")
		}
		if !foundClosed {
			t.Error("expected 'closed' group")
		}
	})

	// ===== Group by priority =====

	t.Run("group_by_priority", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--by-priority")
		groups, ok := m["groups"].([]interface{})
		if !ok || len(groups) == 0 {
			t.Fatal("expected groups array")
		}
		// Should have P1, P2, P3, and P0 groups
		groupNames := make(map[string]bool)
		for _, g := range groups {
			gm := g.(map[string]interface{})
			groupNames[gm["group"].(string)] = true
		}
		if !groupNames["P1"] {
			t.Error("expected P1 group")
		}
	})

	// ===== Group by type =====

	t.Run("group_by_type", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--by-type")
		groups, ok := m["groups"].([]interface{})
		if !ok || len(groups) == 0 {
			t.Fatal("expected groups array")
		}
		groupNames := make(map[string]bool)
		for _, g := range groups {
			gm := g.(map[string]interface{})
			groupNames[gm["group"].(string)] = true
		}
		if !groupNames["bug"] {
			t.Error("expected 'bug' group")
		}
		if !groupNames["task"] {
			t.Error("expected 'task' group")
		}
	})

	// ===== Group by assignee =====

	t.Run("group_by_assignee", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--by-assignee")
		groups, ok := m["groups"].([]interface{})
		if !ok || len(groups) == 0 {
			t.Fatal("expected groups array")
		}
		groupNames := make(map[string]bool)
		for _, g := range groups {
			gm := g.(map[string]interface{})
			groupNames[gm["group"].(string)] = true
		}
		if !groupNames["alice"] {
			t.Error("expected 'alice' group")
		}
		if !groupNames["(unassigned)"] {
			t.Error("expected '(unassigned)' group")
		}
	})

	// ===== Group by label =====

	t.Run("group_by_label", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--by-label")
		groups, ok := m["groups"].([]interface{})
		if !ok || len(groups) == 0 {
			t.Fatal("expected groups array")
		}
		groupNames := make(map[string]bool)
		for _, g := range groups {
			gm := g.(map[string]interface{})
			groupNames[gm["group"].(string)] = true
		}
		if !groupNames["frontend"] {
			t.Error("expected 'frontend' label group")
		}
		if !groupNames["backend"] {
			t.Error("expected 'backend' label group")
		}
	})

	// ===== JSON plain count =====

	t.Run("json_plain_count", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir)
		if _, ok := m["count"]; !ok {
			t.Error("expected 'count' key in JSON output")
		}
	})

	// ===== JSON grouped count =====

	t.Run("json_grouped_count", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--by-status")
		if _, ok := m["total"]; !ok {
			t.Error("expected 'total' key in grouped JSON output")
		}
		if _, ok := m["groups"]; !ok {
			t.Error("expected 'groups' key in grouped JSON output")
		}
	})

	// ===== Error: multiple --by-* flags =====

	t.Run("error_multiple_by_flags", func(t *testing.T) {
		out := bdCountFail(t, bd, dir, "--by-status", "--by-priority")
		if !strings.Contains(out, "only one") {
			t.Errorf("expected 'only one' error, got: %s", out)
		}
	})

	// ===== Combined filters =====

	t.Run("combined_filters", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--status", "open", "--type", "bug", "--assignee", "alice")
		count := int(m["count"].(float64))
		if count < 1 {
			t.Errorf("expected at least 1 open bug assigned to alice, got %d", count)
		}
	})

	// ===== Plain text output =====

	t.Run("plain_text_output", func(t *testing.T) {
		out := strings.TrimSpace(bdCount(t, bd, dir, "--status", "open"))
		// Should be a plain integer
		if len(out) == 0 {
			t.Error("expected non-empty output")
		}
		for _, c := range out {
			if c < '0' || c > '9' {
				t.Errorf("expected plain integer, got: %q", out)
				break
			}
		}
	})

	t.Run("plain_text_grouped_output", func(t *testing.T) {
		out := bdCount(t, bd, dir, "--by-status")
		if !strings.Contains(out, "Total:") {
			t.Errorf("expected 'Total:' in grouped text output, got: %s", out)
		}
		if !strings.Contains(out, "open:") {
			t.Errorf("expected 'open:' in grouped text output, got: %s", out)
		}
	})

	// ===== ID filter =====

	t.Run("filter_by_id", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "ID filter target", "--type", "task")
		m := bdCountJSON(t, bd, dir, "--id", issue.ID)
		count := int(m["count"].(float64))
		if count != 1 {
			t.Errorf("expected exactly 1 issue matching ID, got %d", count)
		}
	})
}

// TestEmbeddedCountIncludeInfra is the CLI-level guard for GH#4387:
// `bd count --include-infra <filters>` must return exactly the cardinality of
// `bd list --include-infra <filters> --all` (modulo list's --limit), including
// the wisps tier (no_history + ephemeral beads) and honoring list's default
// template exclusion. Without the flag, bd count keeps today's durable-only
// semantics.
func TestEmbeddedCountIncludeInfra(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ci")

	// Durable issues tier.
	bdCreate(t, bd, dir, "Infra durable task one", "--type", "task")
	bdCreate(t, bd, dir, "Infra durable task two", "--type", "task")
	bdCreate(t, bd, dir, "Infra durable bug", "--type", "bug")
	closedIssue := bdCreate(t, bd, dir, "Infra durable task closed", "--type", "task")
	bdClose(t, bd, dir, closedIssue.ID)
	// Wisps tier: no_history beads are durable work that bd list --include-infra
	// returns; ephemeral beads are GC-eligible wisps.
	bdCreate(t, bd, dir, "Infra nohistory task one", "--type", "task", "--no-history")
	bdCreate(t, bd, dir, "Infra nohistory task two", "--type", "task", "--no-history")
	bdCreate(t, bd, dir, "Infra ephemeral task", "--type", "task", "--ephemeral")

	countOf := func(args ...string) int {
		t.Helper()
		m := bdCountJSON(t, bd, dir, args...)
		return int(m["count"].(float64))
	}
	// beads-5vanc: the GH#4387 invariant is "count --include-infra <f> counts the
	// same rows bd list --include-infra <f> returns". The reference list must use
	// bd list's DEFAULT status scope (no --all), because beads-9iia aligned
	// `bd count`'s no-status default to `bd list`'s (both now exclude
	// closed/pinned + custom done/frozen when no explicit --status is given).
	// Using --all here would reintroduce the closed row on the list side only,
	// breaking the invariant against count's post-9iia backlog default. An
	// explicit --status <x> still bypasses the default on BOTH commands, so those
	// filter cases are unaffected by dropping --all.
	listCardinality := func(args ...string) int {
		t.Helper()
		fullArgs := append([]string{"--include-infra", "--limit", "0"}, args...)
		return len(bdListJSON(t, bd, dir, fullArgs...))
	}

	t.Run("default_stays_durable_only", func(t *testing.T) {
		// beads-9iia: the no-status default excludes the 1 closed durable task
		// (2 open durable tasks remain), matching bd list's backlog scope. The
		// grand total incl. closed is `bd count --type task --status all`.
		if got := countOf("--type", "task"); got != 2 {
			t.Errorf("bd count --type task = %d, want 2 (open durable tasks only, 9iia default excludes the closed one)", got)
		}
	})

	t.Run("include_infra_counts_wisps_tier", func(t *testing.T) {
		// beads-9iia: 2 OPEN durable tasks (the closed one is excluded by the
		// no-status default) + 2 no_history tasks + 1 ephemeral task = 5.
		if got := countOf("--include-infra", "--type", "task"); got != 5 {
			t.Errorf("bd count --include-infra --type task = %d, want 5", got)
		}
	})

	t.Run("include_infra_matches_list_cardinality", func(t *testing.T) {
		for _, filters := range [][]string{
			nil,
			{"--type", "task"},
			{"--type", "bug"},
			{"--status", "open"},
			{"--status", "closed"},
		} {
			want := listCardinality(filters...)
			got := countOf(append([]string{"--include-infra"}, filters...)...)
			if got != want {
				t.Errorf("bd count --include-infra %v = %d, but bd list --include-infra %v returned %d rows", filters, got, filters, want)
			}
		}
	})

	t.Run("include_infra_grouped_by_type", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--include-infra", "--by-type")
		total := int(m["total"].(float64))
		if want := listCardinality(); total != want {
			t.Errorf("bd count --include-infra --by-type total = %d, want list cardinality %d", total, want)
		}
		groups, ok := m["groups"].([]interface{})
		if !ok {
			t.Fatal("expected groups array")
		}
		byType := make(map[string]int)
		for _, g := range groups {
			gm := g.(map[string]interface{})
			byType[gm["group"].(string)] = int(gm["count"].(float64))
		}
		// beads-9iia: --by-type is a non-status grouping, so the no-status default
		// still applies — the closed durable task is excluded. Task = 2 OPEN
		// durable + 2 no_history + 1 ephemeral = 5.
		if byType["task"] != 5 {
			t.Errorf("grouped task count = %d, want 5 (wisps tier missing from --by-type)", byType["task"])
		}
		if byType["bug"] != 1 {
			t.Errorf("grouped bug count = %d, want 1", byType["bug"])
		}
	})

	t.Run("grouped_without_flag_stays_durable_only", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--by-type")
		groups, ok := m["groups"].([]interface{})
		if !ok {
			t.Fatal("expected groups array")
		}
		for _, g := range groups {
			gm := g.(map[string]interface{})
			if gm["group"] == "task" {
				// beads-9iia: --by-type (non-status grouping) excludes the closed
				// durable task by default → 2 open durable tasks.
				if got := int(gm["count"].(float64)); got != 2 {
					t.Errorf("bd count --by-type task = %d, want 2 (open durable only, 9iia default)", got)
				}
			}
		}
	})
}

// TestEmbeddedCountIncludeInfraGateCaseFold is the beads-y06e regression:
// `bd count --include-infra --type <mixed-case gate>` must return the same count
// as the exact-case `--type gate`. The filter build site normalized the type
// inline (brxo) but did not reassign issueType, so the secondary consumer
// applyCountIncludeInfra saw the raw flag: for a non-exact "GATE" it appended
// "gate" to ExcludeTypes while IssueType was normalized to "gate" — requiring
// gate AND excluding gate -> always 0. The fix canonicalizes issueType so both
// consumers agree.
func TestEmbeddedCountIncludeInfraGateCaseFold(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ci")

	// Seed a couple of gate issues (an internal-use type).
	bdCreate(t, bd, dir, "Gate issue one", "--type", "gate")
	bdCreate(t, bd, dir, "Gate issue two", "--type", "gate")

	countOf := func(args ...string) int {
		t.Helper()
		m := bdCountJSON(t, bd, dir, args...)
		return int(m["count"].(float64))
	}

	exact := countOf("--include-infra", "--type", "gate")
	if exact == 0 {
		t.Fatalf("precondition: bd count --include-infra --type gate = 0, expected the 2 seeded gate issues")
	}
	for _, mixed := range []string{"GATE", "Gate"} {
		if got := countOf("--include-infra", "--type", mixed); got != exact {
			t.Errorf("bd count --include-infra --type %q = %d, want %d (== exact 'gate'); raw-vs-normalized type divergence -> require-gate-AND-exclude-gate = always 0", mixed, got, exact)
		}
	}
}

// TestEmbeddedCountConcurrent exercises count operations concurrently.
func TestEmbeddedCountConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cc")

	// Pre-create issues with varied attributes
	for i := 0; i < 20; i++ {
		args := []string{fmt.Sprintf("concurrent-count-%d", i), "--type", "task"}
		if i%2 == 0 {
			args = append(args, "--assignee", "alice")
		} else {
			args = append(args, "--assignee", "bob")
		}
		if i%3 == 0 {
			args = append(args, "--priority", "1")
		}
		bdCreate(t, bd, dir, args...)
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

			// Each worker runs a different count query
			queries := [][]string{
				{},
				{"--status", "open"},
				{"--assignee", "alice"},
				{"--type", "task"},
				{"--by-status"},
				{"--by-assignee"},
				{"--by-priority"},
				{"--priority", "1"},
			}
			q := queries[worker%len(queries)]

			args := append([]string{"count", "--json"}, q...)
			cmd := exec.Command(bd, args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err != nil {
				r.err = fmt.Errorf("worker %d count %v: %v\nstdout:\n%s\nstderr:\n%s", worker, q, err, stdout.String(), stderr.String())
				results[worker] = r
				return
			}

			// Verify JSON is parseable (parse stdout only; stderr may carry warnings).
			s := strings.TrimSpace(stdout.String())
			start := strings.IndexAny(s, "{")
			if start < 0 {
				r.err = fmt.Errorf("worker %d: no JSON in stdout: %s\nstderr: %s", worker, s, stderr.String())
				results[worker] = r
				return
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(s[start:]), &m); err != nil {
				r.err = fmt.Errorf("worker %d: JSON parse: %v\nstdout: %s\nstderr: %s", worker, err, s, stderr.String())
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

// TestEmbeddedCountRejectsInvalidEnums is the beads-deud teeth: bd count must
// reject invalid --status/--type/--priority values with rc!=0 and a
// valid-values-listing error, mirroring bd list — not silently return 0 exit 0
// (the false-zero that a typo'd script/agent gate would read as an empty set).
// Sibling of beads-pbl7 (ready.go) and beads-brxo (normalize-only, count/search).
func TestEmbeddedCountRejectsInvalidEnums(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cde")
	bdCreate(t, bd, dir, "count enum task", "--type", "task", "--priority", "2")

	cases := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{"invalid_status", []string{"--status", "bogusxyz"}, "invalid status"},
		{"invalid_type", []string{"--type", "notatype"}, "invalid issue type"},
		{"priority_too_high", []string{"--priority", "99"}, "invalid priority"},
		{"priority_negative", []string{"--priority", "-1"}, "invalid priority"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := bdCountFail(t, bd, dir, tc.args...)
			if !strings.Contains(out, tc.wantSub) {
				t.Errorf("bd count %v: expected error containing %q, got:\n%s", tc.args, tc.wantSub, out)
			}
		})
	}

	// Valid values must still succeed (surgical — no regression).
	for _, args := range [][]string{
		{"--status", "open"},
		{"--type", "task"},
		{"--priority", "2"},
	} {
		out := strings.TrimSpace(bdCount(t, bd, dir, args...))
		if out == "" {
			t.Errorf("bd count %v: expected a numeric count, got empty", args)
		}
	}
}

// TestEmbeddedCountPriorityPPrefix is the beads-vcpq regression: bd count's
// --priority / --priority-min / --priority-max must accept the documented
// P0-P4 syntax (not just bare 0-4), mirroring bd list. Previously these were
// IntP/Int flags, so "bd count --priority P2" failed with a raw cobra
// strconv.ParseInt error before any beads code ran — a cross-command
// syntax-parity break on a form bd list documents+accepts. P-prefix must
// resolve identically to the numeric form; out-of-range / non-numeric still
// error (subsuming deud's 0-4 range check via ValidatePriority/ParsePriority).
func TestEmbeddedCountPriorityPPrefix(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cpp")
	bdCreate(t, bd, dir, "cpp p1", "--type", "task", "--priority", "1")
	bdCreate(t, bd, dir, "cpp p2", "--type", "task", "--priority", "2")

	// P-prefix must resolve identically to the numeric form.
	t.Run("priority_pprefix_equals_numeric", func(t *testing.T) {
		gotP := strings.TrimSpace(bdCount(t, bd, dir, "--priority", "P2"))
		gotN := strings.TrimSpace(bdCount(t, bd, dir, "--priority", "2"))
		if gotP != gotN {
			t.Errorf("bd count --priority P2 (%s) != --priority 2 (%s)", gotP, gotN)
		}
		if gotP == "" || gotP == "0" {
			t.Errorf("expected --priority P2 to count the P2 issue, got %q", gotP)
		}
	})

	// --priority-min / --priority-max must accept P-prefix too.
	t.Run("priority_min_pprefix_accepted", func(t *testing.T) {
		gotP := strings.TrimSpace(bdCount(t, bd, dir, "--priority-min", "P1"))
		gotN := strings.TrimSpace(bdCount(t, bd, dir, "--priority-min", "1"))
		if gotP != gotN {
			t.Errorf("bd count --priority-min P1 (%s) != --priority-min 1 (%s)", gotP, gotN)
		}
	})
	t.Run("priority_max_pprefix_accepted", func(t *testing.T) {
		gotP := strings.TrimSpace(bdCount(t, bd, dir, "--priority-max", "P2"))
		gotN := strings.TrimSpace(bdCount(t, bd, dir, "--priority-max", "2"))
		if gotP != gotN {
			t.Errorf("bd count --priority-max P2 (%s) != --priority-max 2 (%s)", gotP, gotN)
		}
	})

	// Invalid values still error (parity with bd list; subsumes the 0-4 range).
	for _, tc := range []struct {
		name string
		flag string
		val  string
	}{
		{"priority_bogus", "--priority", "bogus"},
		{"priority_out_of_range", "--priority", "99"},
		{"priority_min_bogus", "--priority-min", "P9"},
		{"priority_max_bogus", "--priority-max", "notapriority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := bdCountFail(t, bd, dir, tc.flag, tc.val)
			if !strings.Contains(out, "invalid priority") {
				t.Errorf("bd count %s %s: expected 'invalid priority' error, got:\n%s", tc.flag, tc.val, out)
			}
		})
	}
}
