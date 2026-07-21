//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-rb00b: the id-reference tombstone pass (rewrite a live reference to a
// deleted issue → "[deleted:id]" in surviving connected issues) was implemented
// only in the cmd/bd/delete.go single/batch handlers and the domain use-case —
// but the STORAGE-layer delete (issueops.DeleteIssueInTx / DeleteIssuesInTx)
// did NO rewrite. Every bulk-delete path routes through the storage delete
// directly, bypassing both rewriters: bd gc (decay phase) → store.DeleteIssue,
// bd purge / bd prune → store.DeleteIssues, bd mol burn/squash → tx.DeleteIssue.
// So a surviving issue that referenced a gc-decayed issue in its prose kept the
// RAW id, now dangling at a nonexistent issue — the [deleted:id] convention that
// single-delete maintains was silently defeated by the routine space-reclaim ops.
//
// This test proves the fix at the bd gc entry point: after decaying a closed
// issue that a survivor references in its Description, the survivor's text must
// be tombstoned, matching what `bd delete` would have done.
func TestEmbeddedGCDecayTombstonesTextRefs_rb00b(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "GC")

	old := bdCreate(t, bd, dir, "old closed", "--type", "task")
	keeper := bdCreate(t, bd, dir, "keeper", "--type", "task")

	// The survivor references the soon-to-be-decayed issue in prose.
	bdUpdate(t, bd, dir, keeper.ID, "--description", "was blocked by "+old.ID)
	// A real dependency edge too (removed correctly by delete; the TEXT ref is
	// what dangles).
	bdDep(t, bd, dir, "add", keeper.ID, old.ID)

	// Close then gc-decay (delete) the referenced issue.
	closeCmd := exec.Command(bd, "close", old.ID)
	closeCmd.Dir = dir
	closeCmd.Env = bdEnv(dir)
	if out, err := closeCmd.CombinedOutput(); err != nil {
		t.Fatalf("close %s failed: %v\n%s", old.ID, err, out)
	}
	out := bdGC(t, bd, dir, "--force", "--older-than", "0", "--skip-dolt")
	if !strings.Contains(out, "Deleted") && !strings.Contains(out, "deleted") {
		t.Fatalf("gc did not report a decay delete:\n%s", out)
	}

	// The referenced issue is gone; the survivor's description must now carry the
	// [deleted:ID] tombstone, not a dangling live reference.
	got := bdShow(t, bd, dir, keeper.ID)
	wantTomb := "[deleted:" + old.ID + "]"
	if !strings.Contains(got.Description, wantTomb) {
		t.Errorf("gc-decay left a dangling text ref: keeper description = %q, want it to contain %q",
			got.Description, wantTomb)
	}
	if strings.Contains(got.Description, "was blocked by "+old.ID+"") &&
		!strings.Contains(got.Description, wantTomb) {
		t.Errorf("raw id %s still present (not tombstoned): %q", old.ID, got.Description)
	}
}
