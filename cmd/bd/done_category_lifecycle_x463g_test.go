//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedDoneCategoryLifecycle_x463g is the beads-x463g teeth: a custom
// status in the DONE category (e.g. `bd config set status.custom "resolved:done"`)
// is honored by VIEWS (excluded from bd ready / default bd list / bd count) but
// was NOT honored by LIFECYCLE, so a status that "looks done" everywhere the user
// looks silently deadlocked dependents and never auto-closed molecules:
//
//   - DEP-COMPLETENESS: activeBlockerSQL (and its Go mirror
//     isActiveConditionalOrHardBlocker) keyed the blocker-active predicate on the
//     LITERAL `status <> 'closed'`, so a 'blocks' edge to a done-category target
//     stayed active forever → the dependent was permanently un-ready even after
//     `bd recompute-blocked`.
//   - MOLECULE COMPLETION: getMoleculeProgress (mol_current.go) incremented
//     Completed only on `case types.StatusClosed`, so a step moved to a
//     done-category custom status never counted → the molecule never reached
//     Completed==Total → autoCloseCompletedMolecule never fired.
//
// Fix (x463g direction (a) EXTEND): thread the configured done/frozen-category
// custom status names into the is_blocked SQL builder + its Go mirror (kept in
// lockstep per beads-a3hm/bd-hpmw) and into getMoleculeProgress, so a
// done-category status satisfies dependencies and counts toward completion the
// same way literal 'closed' does. Mutation-verify: revert either the SQL/Go
// extension or the getMoleculeProgress extension → the matching leg goes RED.
func TestEmbeddedDoneCategoryLifecycle_x463g(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// setDoneCustomStatus configures a single custom status "resolved" in the
	// DONE category via the real CLI, which routes through SyncCustomStatusesTable
	// (config write path) so the custom_statuses table + status.custom config both
	// carry it — exactly what ResolveCustomStatusesDetailedInTx reads.
	setDoneCustomStatus := func(t *testing.T, dir string) {
		t.Helper()
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
	}

	// Leg 1 — DEP-COMPLETENESS: a 'blocks' edge to a target moved to a
	// done-category custom status must stop blocking the dependent, matching a
	// literal close. Deterministic: it survives `bd recompute-blocked`.
	t.Run("done_category_blocker_unblocks_dependent", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "x4")
		setDoneCustomStatus(t, dir)

		blocker := bdCreate(t, bd, dir, "x463g blocker", "--type", "task")
		dependent := bdCreate(t, bd, dir, "x463g dependent", "--type", "task")

		// blocker blocks dependent: `dep add <dependent> <blocker>`.
		if out, err := bdRunWithFlockRetry(t, bd, dir, "dep", "add", dependent.ID, blocker.ID); err != nil {
			t.Fatalf("dep add failed: %v\n%s", err, out)
		}

		// Baseline: dependent is blocked (absent from ready), blocker is ready.
		if got := bdReadyOutput(t, bd, dir); strings.Contains(got, "x463g dependent") {
			t.Fatalf("baseline: dependent must be blocked (absent from ready) while blocker is open, got:\n%s", got)
		}

		// Move the blocker to the done-category custom status, then recompute.
		bdUpdate(t, bd, dir, blocker.ID, "--status", "resolved")
		if out, err := bdRunWithFlockRetry(t, bd, dir, "recompute-blocked"); err != nil {
			t.Fatalf("recompute-blocked failed: %v\n%s", err, out)
		}

		// The fix: a done-category blocker satisfies the dependency, so the
		// dependent becomes ready. Before x463g the literal `status <> 'closed'`
		// left it permanently blocked.
		if got := bdReadyOutput(t, bd, dir); !strings.Contains(got, "x463g dependent") {
			t.Errorf("dependent should be READY after its blocker moved to a done-category custom status (beads-x463g leg 1): dependent absent from `bd ready`:\n%s", got)
		}
	})

	// Leg 2 — MOLECULE COMPLETION: a molecule step moved to a done-category custom
	// status must count toward Completed, so a molecule whose remaining literal
	// close completes it auto-closes. Uses a mixed molecule (one done-category
	// step + one literal-closed final step) so the existing literal-'closed'
	// autoclose TRIGGER still fires while getMoleculeProgress does the counting —
	// isolating the progress-counting fix from the separate close-transition
	// detection (tracked as a follow-up).
	t.Run("done_category_step_counts_toward_molecule_completion", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "x4m")
		setDoneCustomStatus(t, dir)

		root := bdCreate(t, bd, dir, "x463g molecule root", "--type", "molecule")
		step1 := bdCreate(t, bd, dir, "x463g step 1", "--type", "task")
		step2 := bdCreate(t, bd, dir, "x463g step 2", "--type", "task")
		for _, stepID := range []string{step1.ID, step2.ID} {
			if out, err := bdRunWithFlockRetry(t, bd, dir, "dep", "add", stepID, root.ID, "--type", "parent-child"); err != nil {
				t.Fatalf("dep add (parent-child) %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
			}
		}

		// step1 → done-category custom status (must count as Completed).
		bdUpdate(t, bd, dir, step1.ID, "--status", "resolved")

		// step2 → literal close: fires the autoCloseCompletedMolecule cascade,
		// which re-reads molecule progress. With step1 counted, Completed==Total.
		if out, err := bdRunWithFlockRetry(t, bd, dir, "close", step2.ID, "--reason", "done"); err != nil {
			t.Fatalf("close step2 failed: %v\n%s", err, out)
		}

		root2 := bdShow(t, bd, dir, root.ID)
		if root2.Status != types.StatusClosed {
			t.Errorf("molecule root %s status = %q, want %q — a step in a done-category custom status must count toward completion so the final literal close auto-closes the molecule (beads-x463g leg 2)", root.ID, root2.Status, types.StatusClosed)
		}
	})
}

// bdReadyOutput runs `bd ready` and returns stdout. bd ready exits 0 even with no
// results, so a non-nil error is a genuine failure.
func bdReadyOutput(t *testing.T, bd, dir string) string {
	t.Helper()
	cmd := exec.Command(bd, "ready")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd ready failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return stdout.String()
}
