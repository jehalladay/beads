//go:build cgo

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedUpdateBatchCloseGuardAutoClosingParent is the beads-6b9pz teeth.
//
// beads-bigro (@cf791b036) widened the FORWARD close-time guard ("cannot close
// <parent> with open children") from bare TypeEpic to the shared
// isAutoClosingParentType predicate (epic OR molecule OR ephemeral) — but ONLY
// in close.go / close_proxied_server.go. The SAME forward guard on the
// `bd update --status closed` leg (update.go:537, the beads-zgku guard) and the
// `bd batch` close leg (batch.go:884) were left bare TypeEpic, so a MOLECULE or
// wisp/ephemeral root could still be closed WITH open children via those paths,
// bypassing the invariant the close-guard family (aw9x8/bigro/eth8) protects.
//
// These are subprocess (real `bd`) teeth. Mutation: restore the bare
// `IssueType == types.TypeEpic` test at either site → the molecule/wisp cases
// for that path go from rc!=0 back to rc=0 (RED). The epic control stays GREEN
// (guard preserved) and the plain-task control stays GREEN (close still free) —
// proving the widening is precise on both paths.
func TestEmbeddedUpdateBatchCloseGuardAutoClosingParent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ubg")

	// seedRootWithOpenChild builds a root of the given create args with a single
	// OPEN parent-child step (never closed) — so the root still has an open
	// child at the moment we attempt to close the root.
	seedRootWithOpenChild := func(t *testing.T, prefix string, rootArgs ...string) (root, child *types.Issue) {
		t.Helper()
		root = bdCreate(t, bd, dir, append([]string{prefix + " root"}, rootArgs...)...)
		child = bdCreate(t, bd, dir, prefix+" child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, root.ID, "--type", "parent-child")
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Fatalf("precondition: child %s must be open, got %q", child.ID, got.Status)
		}
		return root, child
	}

	// ===================== `bd update --status closed` =====================

	// (1) MOLECULE root, open child: `bd update <root> --status closed` must
	//     refuse (the update-status=closed twin of the forward guard bigro
	//     covered only for `bd close`), overridable with --force.
	t.Run("update_molecule_close_with_open_child_refuses", func(t *testing.T) {
		root, child := seedRootWithOpenChild(t, "umol", "--type", "molecule")
		out := bdUpdateFail(t, bd, dir, root.ID, "--status", "closed")
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for molecule root via update, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("molecule root must remain open after a refused update-close, got %s", got.Status)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("child must remain open, got %s", got.Status)
		}
	})

	t.Run("update_molecule_close_with_open_child_force_succeeds", func(t *testing.T) {
		root, _ := seedRootWithOpenChild(t, "umolf", "--type", "molecule")
		bdUpdate(t, bd, dir, root.ID, "--status", "closed", "--force")
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("--force should update-close a molecule root with open children, got %s", got.Status)
		}
	})

	// (2) EPHEMERAL (wisp) root, open child: same guard must fire via update.
	t.Run("update_wisp_close_with_open_child_refuses", func(t *testing.T) {
		root, _ := seedRootWithOpenChild(t, "uwisp", "--type", "task", "--ephemeral")
		out := bdUpdateFail(t, bd, dir, root.ID, "--status", "closed")
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for ephemeral root via update, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("ephemeral root must remain open after a refused update-close, got %s", got.Status)
		}
	})

	// (3) EPIC control (regression): the update-status=closed guard already
	//     fired for epics (zgku); it must still fire.
	t.Run("update_epic_close_with_open_child_still_refuses", func(t *testing.T) {
		root, _ := seedRootWithOpenChild(t, "uepic", "--type", "epic")
		out := bdUpdateFail(t, bd, dir, root.ID, "--status", "closed")
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for epic root via update (unchanged), got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("epic root must remain open after a refused update-close, got %s", got.Status)
		}
	})

	// (4) PRECISION control: a plain (non-auto-closing) `task` parent with an
	//     open parent-child child must still update-close FREELY (rc=0).
	t.Run("update_plain_task_parent_close_with_open_child_succeeds", func(t *testing.T) {
		root, child := seedRootWithOpenChild(t, "utask", "--type", "task")
		bdUpdate(t, bd, dir, root.ID, "--status", "closed")
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("a plain task parent with an open child must update-close freely, got %s", got.Status)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("child must remain open, got %s", got.Status)
		}
	})

	// ============================ `bd batch` ============================

	// bdBatchFail runs a batch script expecting a non-zero exit (guard fires).
	bdBatchFail := func(t *testing.T, script string) string {
		t.Helper()
		cmd := exec.Command(bd, "batch")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected bd batch to fail, but it succeeded:\nscript:\n%s\nout:\n%s", script, out)
		}
		return string(out)
	}

	// (5) MOLECULE root, open child: batch `close <root>` must refuse.
	t.Run("batch_molecule_close_with_open_child_refuses", func(t *testing.T) {
		root, child := seedRootWithOpenChild(t, "bmol", "--type", "molecule")
		out := bdBatchFail(t, fmt.Sprintf("close %s\n", root.ID))
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for molecule root via batch, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("molecule root must remain open after a refused batch-close, got %s", got.Status)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("child must remain open, got %s", got.Status)
		}
	})

	// (6) EPHEMERAL (wisp) root, open child: same guard must fire via batch.
	t.Run("batch_wisp_close_with_open_child_refuses", func(t *testing.T) {
		root, _ := seedRootWithOpenChild(t, "bwisp", "--type", "task", "--ephemeral")
		out := bdBatchFail(t, fmt.Sprintf("close %s\n", root.ID))
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for ephemeral root via batch, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("ephemeral root must remain open after a refused batch-close, got %s", got.Status)
		}
	})

	// (7) EPIC control (regression): batch epic close guard unchanged.
	t.Run("batch_epic_close_with_open_child_still_refuses", func(t *testing.T) {
		root, _ := seedRootWithOpenChild(t, "bepic", "--type", "epic")
		out := bdBatchFail(t, fmt.Sprintf("close %s\n", root.ID))
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for epic root via batch (unchanged), got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("epic root must remain open after a refused batch-close, got %s", got.Status)
		}
	})

	// (8) PRECISION control: a plain `task` parent with an open child must still
	//     batch-close FREELY (rc=0).
	t.Run("batch_plain_task_parent_close_with_open_child_succeeds", func(t *testing.T) {
		root, child := seedRootWithOpenChild(t, "btask", "--type", "task")
		bdBatch(t, bd, dir, fmt.Sprintf("close %s\n", root.ID))
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("a plain task parent with an open child must batch-close freely, got %s", got.Status)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("child must remain open, got %s", got.Status)
		}
	})
}
