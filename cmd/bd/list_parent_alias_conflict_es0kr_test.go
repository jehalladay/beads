//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestListParentAliasConflict_es0kr pins the beads-es0kr fix: `bd list`
// documents --filter-parent as an ALIAS for --parent, and the input resolver
// (list_input.go) resolves them by first-match priority (--parent first, then
// --filter-parent). So `bd list --parent PA --filter-parent PB` silently used
// PA and DISCARDED PB with rc0 and no diagnostic — the dz1t8 input-source
// silent-drop class (same as 5vkqz close reason aliases, bscdj dep-add aliases,
// 637yc edit field flags). The fix rejects the two-alias conflict at cobra
// parse time via MarkFlagsMutuallyExclusive.
//
// Both --parent and --filter-parent are non-repeatable String flags, so the
// guard only fires on the genuine conflicting case; legitimate single use of
// either alias is unaffected (regression subtests).
//
// Mutation check: remove the
//   listCmd.MarkFlagsMutuallyExclusive("parent", "filter-parent")
// line in list.go's init() and the reject subtest goes RED (the command
// succeeds rc0, --parent wins, and --filter-parent is silently dropped).
func TestListParentAliasConflict_es0kr(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "lp")

	runList := func(t *testing.T, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{"list"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	// Two distinct parent roots, each with one child.
	pa := bdCreate(t, bd, dir, "parent A", "--type", "epic")
	pb := bdCreate(t, bd, dir, "parent B", "--type", "epic")
	ca := bdCreate(t, bd, dir, "child of A", "--type", "task")
	cb := bdCreate(t, bd, dir, "child of B", "--type", "task")
	depCmdA := exec.Command(bd, "dep", "add", ca.ID, pa.ID, "--type", "parent-child")
	depCmdA.Dir = dir
	depCmdA.Env = bdEnv(dir)
	if out, err := depCmdA.CombinedOutput(); err != nil {
		t.Fatalf("dep add %s %s: %v\n%s", ca.ID, pa.ID, err, out)
	}
	depCmdB := exec.Command(bd, "dep", "add", cb.ID, pb.ID, "--type", "parent-child")
	depCmdB.Dir = dir
	depCmdB.Env = bdEnv(dir)
	if out, err := depCmdB.CombinedOutput(); err != nil {
		t.Fatalf("dep add %s %s: %v\n%s", cb.ID, pb.ID, err, out)
	}

	// The conflicting alias pair with DIFFERENT targets must be rejected loudly,
	// not silently resolve to --parent and drop --filter-parent.
	t.Run("conflict_rejected", func(t *testing.T) {
		out, failed := runList(t, "--parent", pa.ID, "--filter-parent", pb.ID)
		if !failed {
			t.Fatalf("bd list --parent %s --filter-parent %s must be rejected (aliases are mutually exclusive), got success:\n%s",
				pa.ID, pb.ID, out)
		}
		if !strings.Contains(out, "none of the others can be") {
			t.Errorf("expected a mutually-exclusive rejection naming --parent/--filter-parent, got:\n%s", out)
		}
	})

	// Regression: --parent alone still filters to its children.
	t.Run("parent_alone_ok", func(t *testing.T) {
		out, failed := runList(t, "--parent", pa.ID)
		if failed {
			t.Fatalf("bd list --parent %s (single) must succeed, got failure:\n%s", pa.ID, out)
		}
		if !strings.Contains(out, ca.ID) {
			t.Errorf("bd list --parent %s should list child %s, got:\n%s", pa.ID, ca.ID, out)
		}
	})

	// Regression: --filter-parent alone still works (it is a live alias).
	t.Run("filter_parent_alone_ok", func(t *testing.T) {
		out, failed := runList(t, "--filter-parent", pb.ID)
		if failed {
			t.Fatalf("bd list --filter-parent %s (single) must succeed, got failure:\n%s", pb.ID, out)
		}
		if !strings.Contains(out, cb.ID) {
			t.Errorf("bd list --filter-parent %s should list child %s, got:\n%s", pb.ID, cb.ID, out)
		}
	})
}
