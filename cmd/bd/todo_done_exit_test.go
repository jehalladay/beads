//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdTodoDoneExpectFail runs "bd todo done <args>" expecting a nonzero exit and
// returns the combined output.
func bdTodoDoneExpectFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"todo", "done"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd todo done %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdTodoDoneExpectOK runs "bd todo done <args>" expecting a zero exit and
// returns the combined output.
func bdTodoDoneExpectOK(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"todo", "done"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected bd todo done %s to succeed, but failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestEmbeddedTodoDoneExitCode covers beads-xi35: `bd todo done <id>...` looped
// over its args closing each issue, warn+continue on any per-id failure, then
// returned nil unconditionally — so rc=0 even when EVERY id was a ghost (0
// closed) or on partial failure. Because todo done is a state-changing close,
// a `bd todo done $ids || alert` guard silently passed when nothing was closed.
// The command must exit non-zero when any id fails, while still closing (and
// reporting) the ids that resolved (partial-close preserved). Same
// silent-partial-failure class as beads-sw7l / beads-2svv / beads-uscf.
func TestEmbeddedTodoDoneExitCode(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "td")

	t.Run("valid_exits_zero", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "td valid one", "--type", "task")
		bdTodoDoneExpectOK(t, bd, dir, iss.ID)
	})

	t.Run("all_ghost_exits_nonzero", func(t *testing.T) {
		bdTodoDoneExpectFail(t, bd, dir, "td-ghost-a", "td-ghost-b")
	})

	t.Run("partial_exits_nonzero_still_closes_valid", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "td valid two", "--type", "task")
		out := bdTodoDoneExpectFail(t, bd, dir, iss.ID, "td-ghost-z")
		if !strings.Contains(out, iss.ID) {
			t.Errorf("expected valid issue %s still closed/reported on partial failure, got:\n%s", iss.ID, out)
		}
	})
}
