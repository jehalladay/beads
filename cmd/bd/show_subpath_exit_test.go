//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdShowSubExpectFail runs "bd show <args>" expecting a nonzero exit and returns
// the combined output.
func bdShowSubExpectFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"show"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd show %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdShowSubExpectOK runs "bd show <args>" expecting a zero exit and returns the
// combined output.
func bdShowSubExpectOK(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"show"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected bd show %s to succeed, but failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestEmbeddedShowSubpathExitCode covers beads-2svv: the flag-gated read
// subpaths of `bd show` (--refs, --children, --as-of) swallowed id-resolution
// failures entirely, returning rc=0 even when EVERY requested id was a ghost —
// worse than plain `bd show` (which exits rc=1 when all ids fail) and its
// variadic twin beads-sw7l. Each subpath must exit non-zero when any id fails,
// while still displaying the results that were found (partial display
// preserved).
func TestEmbeddedShowSubpathExitCode(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sp")

	parent := bdCreate(t, bd, dir, "sp parent", "--type", "epic")
	child := bdCreate(t, bd, dir, "sp child", "--type", "task")
	// Establish a parent/child edge so the --refs and --children subpaths have
	// real data to display on the valid id (partial-display assertions).
	bdDepAdd(t, bd, dir, child.ID, parent.ID, "--type", "parent-child")

	// --- show --refs ---
	t.Run("refs_valid_exits_zero", func(t *testing.T) {
		bdShowSubExpectOK(t, bd, dir, "--refs", parent.ID)
	})
	t.Run("refs_all_ghost_exits_nonzero", func(t *testing.T) {
		bdShowSubExpectFail(t, bd, dir, "--refs", "sp-ghost-a", "sp-ghost-b")
	})
	t.Run("refs_partial_exits_nonzero_still_shows_valid", func(t *testing.T) {
		out := bdShowSubExpectFail(t, bd, dir, "--refs", parent.ID, "sp-ghost")
		if !strings.Contains(out, parent.ID) {
			t.Errorf("expected valid issue %s still shown on partial --refs failure, got:\n%s", parent.ID, out)
		}
	})

	// --- show --children ---
	t.Run("children_valid_exits_zero", func(t *testing.T) {
		bdShowSubExpectOK(t, bd, dir, "--children", parent.ID)
	})
	t.Run("children_all_ghost_exits_nonzero", func(t *testing.T) {
		bdShowSubExpectFail(t, bd, dir, "--children", "sp-ghost-a", "sp-ghost-b")
	})
	t.Run("children_partial_exits_nonzero_still_shows_valid", func(t *testing.T) {
		out := bdShowSubExpectFail(t, bd, dir, "--children", parent.ID, "sp-ghost")
		if !strings.Contains(out, child.ID) {
			t.Errorf("expected child %s still shown on partial --children failure, got:\n%s", child.ID, out)
		}
	})

	// --- show --as-of ---
	t.Run("asof_valid_exits_zero", func(t *testing.T) {
		bdShowSubExpectOK(t, bd, dir, "--as-of", "HEAD", parent.ID)
	})
	t.Run("asof_single_ghost_exits_nonzero", func(t *testing.T) {
		bdShowSubExpectFail(t, bd, dir, "--as-of", "HEAD", "sp-ghost")
	})
	t.Run("asof_partial_exits_nonzero_still_shows_valid", func(t *testing.T) {
		out := bdShowSubExpectFail(t, bd, dir, "--as-of", "HEAD", parent.ID, "sp-ghost")
		if !strings.Contains(out, parent.ID) {
			t.Errorf("expected valid issue %s still shown on partial --as-of failure, got:\n%s", parent.ID, out)
		}
	})
}
