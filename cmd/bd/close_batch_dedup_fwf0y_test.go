//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-fwf0y: a repeated issue id in one `bd close X X` batch must close +
// report X exactly once. The close loop hydrates every result's Issue snapshot
// (resolveCloseTargets) BEFORE the first CloseIssue write fires, so the dr3
// already-closed guard (issue.Status == StatusClosed) checks a STALE snapshot
// for the 2nd occurrence of X (still open) → without the command-entry dedup it
// prints a phantom 2nd "✓ Closed" glyph and double-counts closedIssues[] under
// --json, even though only one write lands (the DB stays correct; the reporting
// partition is corrupted). This is the in-batch dup dr3 is blind to (dr3 only
// guards CROSS-invocation already-closed).
//
// MUTATION MATRIX: neuter the `if len(resolvedIDs) > 1 { ... }` dedup block in
// close.go → close_repeated_id_* go RED (2 glyphs / closedIssues len==2);
// close_distinct_ids_both_closed stays GREEN either way.
func TestEmbeddedCloseBatchDedupFwf0y(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "fw")

	t.Run("close_repeated_id_closes_once", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "fwf0y repeated", "--type", "task")

		// Plain-text path: the repeated id must yield exactly ONE "✓ Closed"
		// glyph, not a phantom second success line for the same id.
		out := bdClose(t, bd, dir, issue.ID, issue.ID)
		if n := strings.Count(out, "Closed "+issue.ID); n != 1 {
			t.Errorf("bd close X X: want exactly 1 '✓ Closed %s' glyph, got %d\n%s", issue.ID, n, out)
		}
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("expected status closed, got %s", got.Status)
		}
	})

	t.Run("close_repeated_id_json_len_one", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "fwf0y repeated json", "--type", "task")

		// --json path: the baseline close --json emits the closed[] array; a
		// repeated id must appear exactly once, not double-counted.
		cmd := exec.Command(bd, "close", issue.ID, issue.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd close X X --json failed: %v\n%s", err, out)
		}
		var closed []map[string]interface{}
		if err := json.Unmarshal(out, &closed); err != nil {
			t.Fatalf("parse close --json array: %v\n%s", err, out)
		}
		if len(closed) != 1 {
			t.Errorf("bd close X X --json: want closed[] len 1, got %d\n%s", len(closed), out)
		}
	})

	t.Run("close_distinct_ids_both_closed", func(t *testing.T) {
		// Negative control: two DISTINCT ids must both close + both report.
		issue1 := bdCreate(t, bd, dir, "fwf0y distinct 1", "--type", "task")
		issue2 := bdCreate(t, bd, dir, "fwf0y distinct 2", "--type", "task")

		cmd := exec.Command(bd, "close", issue1.ID, issue2.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd close X Y --json failed: %v\n%s", err, out)
		}
		var closed []map[string]interface{}
		if err := json.Unmarshal(out, &closed); err != nil {
			t.Fatalf("parse close --json array: %v\n%s", err, out)
		}
		if len(closed) != 2 {
			t.Errorf("bd close X Y --json: want closed[] len 2, got %d\n%s", len(closed), out)
		}
		if bdShow(t, bd, dir, issue1.ID).Status != types.StatusClosed {
			t.Errorf("issue1 not closed")
		}
		if bdShow(t, bd, dir, issue2.ID).Status != types.StatusClosed {
			t.Errorf("issue2 not closed")
		}
	})

	t.Run("close_repeated_id_per_issue_reason_stays_aligned", func(t *testing.T) {
		// The dedup collapses the index-parallel reasons slice too, so a
		// per-issue reason on a repeated id must not desync (first wins).
		issue := bdCreate(t, bd, dir, "fwf0y repeated reason", "--type", "task")
		bdClose(t, bd, dir, issue.ID, "--reason", "first", issue.ID, "--reason", "second")
		got := bdShow(t, bd, dir, issue.ID)
		if got.CloseReason != "first" {
			t.Errorf("repeated-id per-issue reason: want first-occurrence 'first', got %q", got.CloseReason)
		}
	})
}
