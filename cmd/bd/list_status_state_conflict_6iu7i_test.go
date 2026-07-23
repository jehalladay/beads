//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestListStatusStateConflict_6iu7i pins the beads-6iu7i fix: `bd list`
// documents --state as an ALIAS for --status, and the input resolver
// (list_input.go:102-104) resolves them by first-match priority (--status
// first, then --state). So `bd list --status open --state closed` silently
// used "open" and DISCARDED --state with rc0 and no diagnostic — the dz1t8
// input-source silent-drop class (same as --parent/--filter-parent es0kr,
// bd close reason aliases 5vkqz, bd dep add aliases bscdj). The fix rejects the
// two-alias conflict at cobra parse time via MarkFlagsMutuallyExclusive.
//
// Both --status and --state are non-repeatable String flags, so the guard only
// fires on the genuine conflicting case; the comma-separated single-flag
// multi-status form (--status open,in_progress) and legitimate single use of
// either alias are unaffected (regression subtests).
//
// Mutation check: remove the
//   listCmd.MarkFlagsMutuallyExclusive("status", "state")
// line in list.go's init() and the reject subtest goes RED (the command
// succeeds rc0, --status wins, and --state is silently dropped).
func TestListStatusStateConflict_6iu7i(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ss")

	runList := func(t *testing.T, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{"list"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	// One open issue, one closed issue.
	openIssue := bdCreate(t, bd, dir, "still open", "--type", "task")
	closedIssue := bdCreate(t, bd, dir, "now closed", "--type", "task")
	closeCmd := exec.Command(bd, "close", closedIssue.ID, "--reason", "done")
	closeCmd.Dir = dir
	closeCmd.Env = bdEnv(dir)
	if out, err := closeCmd.CombinedOutput(); err != nil {
		t.Fatalf("close %s: %v\n%s", closedIssue.ID, err, out)
	}

	// The conflicting alias pair with DIFFERENT values must be rejected loudly,
	// not silently resolve to --status and drop --state.
	t.Run("conflict_rejected", func(t *testing.T) {
		out, failed := runList(t, "--status", "open", "--state", "closed")
		if !failed {
			t.Fatalf("bd list --status open --state closed must be rejected (aliases are mutually exclusive), got success:\n%s", out)
		}
		if !strings.Contains(out, "none of the others can be") {
			t.Errorf("expected a mutually-exclusive rejection naming --status/--state, got:\n%s", out)
		}
	})

	// Regression: --status alone still filters.
	t.Run("status_alone_ok", func(t *testing.T) {
		out, failed := runList(t, "--status", "open")
		if failed {
			t.Fatalf("bd list --status open (single) must succeed, got failure:\n%s", out)
		}
		if !strings.Contains(out, openIssue.ID) {
			t.Errorf("bd list --status open should list open issue %s, got:\n%s", openIssue.ID, out)
		}
		if strings.Contains(out, closedIssue.ID) {
			t.Errorf("bd list --status open should NOT list closed issue %s, got:\n%s", closedIssue.ID, out)
		}
	})

	// Regression: --state alone still works (it is a live alias).
	t.Run("state_alone_ok", func(t *testing.T) {
		out, failed := runList(t, "--state", "closed")
		if failed {
			t.Fatalf("bd list --state closed (single) must succeed, got failure:\n%s", out)
		}
		if !strings.Contains(out, closedIssue.ID) {
			t.Errorf("bd list --state closed should list closed issue %s, got:\n%s", closedIssue.ID, out)
		}
	})

	// Regression: comma-separated multi-status on a SINGLE flag is not a conflict.
	t.Run("comma_multi_status_ok", func(t *testing.T) {
		out, failed := runList(t, "--status", "open,closed")
		if failed {
			t.Fatalf("bd list --status open,closed (single flag) must succeed, got failure:\n%s", out)
		}
		if !strings.Contains(out, openIssue.ID) || !strings.Contains(out, closedIssue.ID) {
			t.Errorf("bd list --status open,closed should list both issues, got:\n%s", out)
		}
	})
}
