//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedCountSearchMultiStatus covers beads-ybc7: `bd count` and
// `bd search` now accept a comma-separated `--status open,closed` as an OR
// (IN) filter, matching `bd list`'s documented multi-status behavior
// (list_filter.go). Before the fix, count/search treated the whole
// "open,closed" string as a single status value — pre-deud that silently
// matched nothing (count=0, exit 0), and post-deud it became a hard "invalid
// status" error — either way lacking bd list's OR parity. A single value still
// takes the scalar path; each element of a multi-status value is validated, so
// a typo inside the list still fails loud.
func TestEmbeddedCountSearchMultiStatus(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ms")

	// Seed three issues sharing a query token, one per target status:
	// - openIssue stays open
	// - closedIssue is closed
	// - inProgIssue is moved to in_progress
	openIssue := bdCreate(t, bd, dir, "ybc7multi alpha", "--type", "task", "--priority", "2")
	closedIssue := bdCreate(t, bd, dir, "ybc7multi beta", "--type", "task", "--priority", "2")
	inProgIssue := bdCreate(t, bd, dir, "ybc7multi gamma", "--type", "task", "--priority", "2")
	bdClose(t, bd, dir, closedIssue.ID)
	bdUpdate(t, bd, dir, inProgIssue.ID, "--status", "in_progress")
	_ = openIssue

	// --- count ---

	t.Run("count_single_status_still_scalar", func(t *testing.T) {
		// Regression guard: a single value keeps the scalar filter.Status path.
		m := bdCountJSON(t, bd, dir, "--status", "open")
		if got := int(m["count"].(float64)); got != 1 {
			t.Errorf("count --status open: want 1 (the open issue), got %d", got)
		}
	})

	t.Run("count_multi_status_or", func(t *testing.T) {
		// The bug: "open,closed" must count BOTH (2), not fail / match nothing.
		m := bdCountJSON(t, bd, dir, "--status", "open,closed")
		if got := int(m["count"].(float64)); got != 2 {
			t.Errorf("count --status open,closed: want 2 (open OR closed), got %d", got)
		}
	})

	t.Run("count_multi_status_three", func(t *testing.T) {
		m := bdCountJSON(t, bd, dir, "--status", "open,closed,in_progress")
		if got := int(m["count"].(float64)); got != 3 {
			t.Errorf("count --status open,closed,in_progress: want 3, got %d", got)
		}
	})

	t.Run("count_multi_status_with_spaces", func(t *testing.T) {
		// Whitespace around each element is trimmed (mirrors bd list).
		m := bdCountJSON(t, bd, dir, "--status", "open, closed")
		if got := int(m["count"].(float64)); got != 2 {
			t.Errorf("count --status 'open, closed': want 2, got %d", got)
		}
	})

	t.Run("count_multi_status_typo_rejected", func(t *testing.T) {
		// A typo inside the multi-status list must still fail loud (rc!=0),
		// not silently drop the bad element.
		out := bdCountFail(t, bd, dir, "--status", "open,bogusxyz")
		if !strings.Contains(out, "invalid status") {
			t.Errorf("count --status open,bogusxyz: want an 'invalid status' error, got:\n%s", out)
		}
	})

	// --- search ---

	t.Run("search_single_status_still_scalar", func(t *testing.T) {
		results := bdSearchJSON(t, bd, dir, "ybc7multi", "--status", "open")
		if len(results) != 1 {
			t.Errorf("search ybc7multi --status open: want 1, got %d", len(results))
		}
	})

	t.Run("search_multi_status_or", func(t *testing.T) {
		results := bdSearchJSON(t, bd, dir, "ybc7multi", "--status", "open,closed")
		if len(results) != 2 {
			t.Errorf("search ybc7multi --status open,closed: want 2 (open OR closed), got %d", len(results))
		}
	})

	t.Run("search_multi_status_typo_rejected", func(t *testing.T) {
		cmd := exec.Command(bd, "search", "ybc7multi", "--status", "open,bogusxyz")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected search --status open,bogusxyz to fail, got:\n%s", out)
		}
		if !strings.Contains(string(out), "invalid status") {
			t.Errorf("search --status open,bogusxyz: want 'invalid status' error, got:\n%s", out)
		}
	})
}
