//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-poj2t (batch-parity family; eth8-family sibling): the single-command
// path (`bd dep add <child> <parent> --type parent-child`, dep.go) refuses —
// without --force — attaching an OPEN child to a CLOSED auto-closing parent
// (epic/molecule/wisp, via the shared isAutoClosingParentType helper widened by
// beads-aw9x8), because it would recreate the closed-parent-with-open-child
// state the close-guard family exists to prevent. The batch preflight
// (guardBatchDepAdds, batch.go) is the batch leg of that surface; before this
// fix `bd batch dep.add <child> <parent> parent-child` wrote straight to
// tx.AddDependency below the CLI guard layer and silently landed the edge
// (rc=0, closed parent left with an open child).
//
// End-to-end through the ACTUAL `bd batch` subprocess (NOT a tx-helper, which
// would false-green by skipping the CLI-layer preflight entirely — see the
// batch-parity family lessons). MUTATION-VERIFIED: removing the
// guardBatchDepAdds call lets the batch add the edge (rc=0, edge present).
func TestEmbeddedBatchDepAddClosedParentGuard_poj2t(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	runBatch := func(t *testing.T, dir, stdin string, extraArgs ...string) (combined string, err error) {
		t.Helper()
		args := append([]string{"batch"}, extraArgs...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader(stdin)
		stdout, stderr, e := runCommandBuffers(t, cmd)
		return stdout.String() + stderr.String(), e
	}

	edgeExists := func(t *testing.T, dir, child, parent string) bool {
		t.Helper()
		for _, d := range showDeps(t, bd, dir, child) {
			if d.ID == parent {
				return true
			}
		}
		return false
	}

	newClosedEpic := func(t *testing.T, dir, prefix string) string {
		t.Helper()
		epic := bdCreate(t, bd, dir, prefix+" epic", "--type", "epic")
		bdClose(t, bd, dir, epic.ID) // no children yet → clean close
		if got := bdShow(t, bd, dir, epic.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: epic %s should be closed, got %s", epic.ID, got.Status)
		}
		return epic.ID
	}

	// CONTROL: single `bd dep add <open-child> <closed-epic> --type parent-child`
	// is refused. Establishes the authoritative behavior batch must mirror.
	t.Run("single_depadd_refuses_closed_parent", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ps")
		epic := newClosedEpic(t, dir, "ps")
		child := bdCreate(t, bd, dir, "ps child", "--type", "task") // open

		out := bdDepAddFail(t, bd, dir, child.ID, epic, "--type", "parent-child")
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected a 'closed parent' guard error from single dep add, got:\n%s", out)
		}
		if edgeExists(t, dir, child.ID, epic) {
			t.Errorf("single dep add to a closed parent should not create the edge")
		}
	})

	// FIX: `bd batch dep.add <open-child> <closed-epic> parent-child` must ALSO
	// be refused (non-zero rc, edge not created).
	t.Run("batch_depadd_refuses_closed_parent", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pb")
		epic := newClosedEpic(t, dir, "pb")
		child := bdCreate(t, bd, dir, "pb child", "--type", "task") // open

		combined, err := runBatch(t, dir, "dep add "+child.ID+" "+epic+" parent-child\n")
		if err == nil {
			t.Fatalf("expected batch dep add under a closed parent to FAIL, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "closed parent") {
			t.Errorf("expected a 'closed parent' guard error from batch dep add, got:\n%s", combined)
		}
		if edgeExists(t, dir, child.ID, epic) {
			t.Errorf("batch dep add to a closed parent should not create the edge (poj2t)")
		}
	})

	// --force override: batch --force skips the guard (parity with
	// `bd dep add --force`), so the edge is created.
	t.Run("batch_force_overrides_depadd_guard", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pf")
		epic := newClosedEpic(t, dir, "pf")
		child := bdCreate(t, bd, dir, "pf child", "--type", "task")

		combined, err := runBatch(t, dir, "dep add "+child.ID+" "+epic+" parent-child\n", "--force")
		if err != nil {
			t.Fatalf("expected batch --force to add the edge, got error: %v\n%s", err, combined)
		}
		if !edgeExists(t, dir, child.ID, epic) {
			t.Errorf("batch --force should add the parent-child edge to a closed parent\n%s", combined)
		}
	})

	// beads-aw9x8 widened the guard from epic-only to the full auto-closing set
	// (epic OR molecule OR ephemeral) via the shared isAutoClosingParentType
	// helper. guardBatchDepAdds calls it, so the batch path inherits the widening
	// — this subtest proves it: batch-adding an open child under a closed
	// MOLECULE root must be refused.
	t.Run("batch_depadd_molecule_root_refuses", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pm")
		root := bdCreate(t, bd, dir, "pm mol root", "--type", "molecule")
		step := bdCreate(t, bd, dir, "pm mol step", "--type", "task")
		bdDepAdd(t, bd, dir, step.ID, root.ID, "--type", "parent-child")
		bdClose(t, bd, dir, step.ID) // last step complete → molecule root auto-closes
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: molecule root %s should auto-close, got %s", root.ID, got.Status)
		}
		newChild := bdCreate(t, bd, dir, "pm new open child", "--type", "task") // open

		combined, err := runBatch(t, dir, "dep add "+newChild.ID+" "+root.ID+" parent-child\n")
		if err == nil {
			t.Fatalf("expected batch dep add under a closed molecule root to FAIL, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "closed parent") {
			t.Errorf("expected a 'closed parent' guard error for the molecule root, got:\n%s", combined)
		}
		if edgeExists(t, dir, newChild.ID, root.ID) {
			t.Errorf("batch dep add to a closed molecule root should not create the edge (poj2t/aw9x8)")
		}
	})

	// Batch-aware: reopening the parent AND adding the child in the same batch is
	// consistent (the parent is no longer closed by commit time) → allowed.
	t.Run("batch_reopen_parent_and_add_child_together_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pt")
		epic := newClosedEpic(t, dir, "pt")
		child := bdCreate(t, bd, dir, "pt child", "--type", "task") // open

		combined, err := runBatch(t, dir,
			"update "+epic+" status=open\ndep add "+child.ID+" "+epic+" parent-child\n")
		if err != nil {
			t.Fatalf("expected reopen-parent + add-child together to succeed, got error: %v\n%s", err, combined)
		}
		if !edgeExists(t, dir, child.ID, epic) {
			t.Errorf("edge should be added when the parent is reopened in the same batch\n%s", combined)
		}
		if got := bdShow(t, bd, dir, epic); got.Status != types.StatusOpen {
			t.Errorf("epic should be OPEN after the batch, got %s", got.Status)
		}
	})

	// Negative (no false positive): a `blocks` edge is unaffected — the guard is
	// specific to the parent-child (epic-membership) relationship. Use a
	// closed→closed pair so the cross-type blocking rule doesn't independently
	// reject it; the point is the closed-parent guard does not fire on non-pc.
	t.Run("batch_blocks_edge_not_guarded", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pk")
		a := bdCreate(t, bd, dir, "pk a", "--type", "task")
		b := bdCreate(t, bd, dir, "pk b", "--type", "task")

		combined, err := runBatch(t, dir, "dep add "+a.ID+" "+b.ID+" blocks\n")
		if err != nil {
			t.Fatalf("a blocks edge must not be touched by the parent-child guard, got error: %v\n%s", err, combined)
		}
		if !edgeExists(t, dir, a.ID, b.ID) {
			t.Errorf("blocks edge should be created (not guarded by poj2t)\n%s", combined)
		}
	})

	// Negative (no false positive): open child → OPEN epic is the normal case.
	t.Run("batch_depadd_under_open_epic_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "po")
		epic := bdCreate(t, bd, dir, "po epic", "--type", "epic") // stays open
		child := bdCreate(t, bd, dir, "po child", "--type", "task")

		combined, err := runBatch(t, dir, "dep add "+child.ID+" "+epic.ID+" parent-child\n")
		if err != nil {
			t.Fatalf("dep add under an OPEN epic must be allowed, got error: %v\n%s", err, combined)
		}
		if !edgeExists(t, dir, child.ID, epic.ID) {
			t.Errorf("open child under an open epic should attach\n%s", combined)
		}
	})
}
