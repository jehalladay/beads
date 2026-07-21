//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-8kixi: `bd mol squash` auto-closes an ephemeral (wisp) molecule ROOT via
// tx.CloseIssue inside squashMolecule's `transact` (mol_squash.go) but emitted no
// auditStatusChange, so the squash-driven root close recorded only the DB
// EventClosed row — NOT the GC-survivable audit-FILE trail
// (.beads/interactions.jsonl, beads-n4sn) that a plain `bd close` writes. After a
// Dolt GC flatten the squashed root's close vanished from the durable record
// while a directly-closed issue's did not — the same n4sn divergence beads-r3m8v
// (supersede/duplicate source), beads-zt47w (auto-closed molecule root) and
// beads-iwzua (epic close-eligible) fixed for the other CloseIssue-bypass cascade
// members. mol squash is the last unmined member.
//
// End-to-end through the real `bd` subprocess: audit.LogFieldChange writes a
// cwd-based file (beads.FindBeadsDir walks up from cwd), so only the real cmd
// handler with cmd.Dir set exercises it — the in-process squashMolecule test
// (mol_squash_embedded_test.go) can't. mol squash is direct-only (proxied mode is
// rejected upstream), so this single direct leg covers the fix.
// MUTATION-VERIFIED: removing the auditStatusChange call after `transact` in
// squashMolecule drops the entry → the parity assertion goes RED.

func TestMolSquash_WritesGCSurvivableAuditTrail_8kixi(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "msa")

	// CONTROL: a single-path close writes the status audit trail (proves the
	// harness reads the cwd audit FILE correctly).
	ctrl := bdCreate(t, bd, dir, "control close", "--type", "task")
	if cmd := exec.Command(bd, "close", ctrl.ID, "-r", "control"); true {
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if _, stderr, err := runCommandBuffers(t, cmd); err != nil {
			t.Fatalf("CONTROL bd close %s failed: %v\nstderr:\n%s", ctrl.ID, err, stderr.String())
		}
	}
	if !auditHasStatusChange(t, dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	// Build an ephemeral (wisp) molecule: an ephemeral root with one ephemeral
	// child linked parent-child. The child is left OPEN on purpose — closing it
	// would trip the `bd close` step-completion cascade (autoCloseCompletedMolecule,
	// beads-zt47w) which auto-closes AND audits the root before squash runs,
	// masking squash's own emit (a false-green). `bd mol squash` collapses the
	// ephemeral children into a digest and force-closes the wisp root
	// (squashMolecule's root.Ephemeral leg) — that is the sole open→closed
	// transition here, so the audit entry can only come from the squash fix.
	root := bdCreate(t, bd, dir, "wisp molecule root", "--type", "epic", "--ephemeral")
	bdCreate(t, bd, dir, "wisp step", "--type", "task", "--parent", root.ID, "--ephemeral")

	sqCmd := exec.Command(bd, "mol", "squash", root.ID)
	sqCmd.Dir = dir
	sqCmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, sqCmd); err != nil {
		t.Fatalf("`bd mol squash %s` failed: %v\nstdout:\n%s\nstderr:\n%s", root.ID, err, stdout.String(), stderr.String())
	}

	if bdShow(t, bd, dir, root.ID).Status != types.StatusClosed {
		t.Fatalf("`bd mol squash` did not auto-close wisp molecule root %s", root.ID)
	}
	if !auditHasStatusChange(t, dir, root.ID, "closed") {
		t.Errorf("`bd mol squash` wisp-root auto-close did not write a GC-survivable audit field_change to status=closed for root %s (beads-8kixi) — parity with bd close / manual + auto molecule-close cascade broken", root.ID)
	}
}
