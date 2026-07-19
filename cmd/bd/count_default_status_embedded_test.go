//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedCountDefaultStatusScope proves `bd count` (no flags) excludes
// closed/pinned by default, matching `bd list` (beads-9iia). Before the fix
// count built types.IssueFilter{} with no default-exclude, so a plain count
// included closed work — disagreeing with the sibling `bd list` total.
func TestEmbeddedCountDefaultStatusScope(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cds")

	// 3 open + 1 closed.
	bdCreate(t, bd, dir, "open 1", "--type", "task")
	bdCreate(t, bd, dir, "open 2", "--type", "task")
	bdCreate(t, bd, dir, "open 3", "--type", "task")
	toClose := bdCreate(t, bd, dir, "to close", "--type", "task")
	bdClose(t, bd, dir, toClose.ID)

	t.Run("no_flags_excludes_closed", func(t *testing.T) {
		// 3 open, closed excluded → 3 (was 4 before the fix).
		if got := strings.TrimSpace(bdCount(t, bd, dir)); got != "3" {
			t.Errorf("bd count (no flags) = %q, want 3 (closed excluded like bd list)", got)
		}
	})

	t.Run("status_all_includes_closed", func(t *testing.T) {
		if got := strings.TrimSpace(bdCount(t, bd, dir, "--status", "all")); got != "4" {
			t.Errorf("bd count --status all = %q, want 4 (closed included)", got)
		}
	})

	t.Run("explicit_closed_status", func(t *testing.T) {
		if got := strings.TrimSpace(bdCount(t, bd, dir, "--status", "closed")); got != "1" {
			t.Errorf("bd count --status closed = %q, want 1", got)
		}
	})

	t.Run("by_status_still_shows_closed", func(t *testing.T) {
		out := bdCount(t, bd, dir, "--by-status")
		if !strings.Contains(out, "closed") {
			t.Errorf("bd count --by-status should still bucket closed:\n%s", out)
		}
	})
}
