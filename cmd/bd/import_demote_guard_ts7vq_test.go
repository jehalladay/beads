//go:build cgo

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedImportParentDemoteGuard_ts7vq is the beads-ts7vq teeth: the
// close-guard family's IMPORT sibling. The epic/molecule/wisp demote guard
// (wouldRemainAutoClosingParent, cmd/bd/close.go — beads-2hkd/l7l3j) is enforced
// on the `bd update --type` command path, so a direct `bd update <epic> --type
// task` with an open child is refused without --force. But `bd import` applies
// the incoming issue_type field-wise through its upsert with NO demote check —
// so an import row (including under --allow-stale, which requires no --force)
// that flips an auto-closing parent with open children to a non-auto-closing
// type silently demotes it, recreating the forbidden closed-parent-with-open-
// child state on the next parent close. Same class as 2hkd (update --type),
// aw9x8 (type-filter), b0tw (reopen): a guard the direct-single command enforces
// leaking on the bulk/import path.
//
// The fix (guardImportParentDemote in import_shared.go) REVERTS the incoming
// type to the local value for the offending rows — import's skip-and-report /
// preserve-on-absent model, not an all-or-nothing abort — and reports the ids so
// the (otherwise silent) bypass is visible. All other fields on the row still
// import.
//
// Driven END-TO-END through the real `bd import` subprocess so the teeth
// exercise the actual upsert + guard plumbing (a core-func unit test would miss
// the CLI wiring). MUTATION-VERIFIED: neuter guardImportParentDemote (e.g.
// `return nil, nil` at its top, or drop the `issue.IssueType = local.IssueType`
// revert) → epic_with_open_child_demote_reverted + allow_stale_demote_reverted
// go RED (type demotes to task and the subsequent parent close SUCCEEDS with an
// open child).
func TestEmbeddedImportParentDemoteGuard_ts7vq(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A far-future updated_at makes the import line strictly newer than the
	// just-created local row, so the default stale guard never skips it and the
	// upsert (the thing under test) actually runs on the guarded path.
	const newerTS = "2027-06-01T00:00:00Z"

	// seedEpicWithOpenChild creates an epic + an OPEN parent-child child and
	// returns both. The epic is a real auto-closing parent type; the direct
	// `bd update <epic> --type task` demote is refused while the child is open.
	seedEpicWithOpenChild := func(t *testing.T, dir, prefix string) (epic, child *types.Issue) {
		t.Helper()
		epic = bdCreate(t, bd, dir, prefix+" epic", "--type", "epic")
		child = bdCreate(t, bd, dir, prefix+" child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		return epic, child
	}

	// (1) Import row that demotes an epic-with-open-child to task is REVERTED:
	//     the local type stays epic, the row is reported, and the subsequent
	//     parent close is still refused (the guard the family exists to enforce
	//     is intact). This is the exact ts7vq repro through `bd import`.
	t.Run("epic_with_open_child_demote_reverted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "idg")
		epic, _ := seedEpicWithOpenChild(t, dir, "idg")

		// Sanity: the DIRECT demote is refused (the guard we mirror works).
		out := bdUpdateFail(t, bd, dir, epic.ID, "--type", "task")
		if !strings.Contains(out, "cannot demote") {
			t.Fatalf("precondition: direct `bd update --type task` on epic w/ open child must be refused; got:\n%s", out)
		}

		// IMPORT the same demote (newer-ts line flipping issue_type epic->task).
		jsonl := filepath.Join(t.TempDir(), "demote.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"idg epic","issue_type":"task","updated_at":%q}`, epic.ID, newerTS))
		importOut := bdImport(t, bd, dir, jsonl)

		// The type must NOT have demoted — the guard reverted it to epic.
		if got := bdShow(t, bd, dir, epic.ID); got.IssueType != types.TypeEpic {
			t.Errorf("beads-ts7vq: import silently demoted epic w/ open child to %q — must revert to epic [BUG]", got.IssueType)
		}
		// The revert must be reported (not silent).
		if !strings.Contains(importOut, epic.ID) || !strings.Contains(importOut, "Kept parent type") {
			t.Errorf("beads-ts7vq: import demote-revert must report the id %s; got:\n%s", epic.ID, importOut)
		}
		// The close-guard the demote would have bypassed still fires.
		closeOut := bdCloseFail(t, bd, dir, epic.ID)
		if !strings.Contains(closeOut, "open child") && !strings.Contains(closeOut, "--force") {
			t.Errorf("beads-ts7vq: after the reverted demote, `bd close` on the epic w/ open child must still be refused; got:\n%s", closeOut)
		}
	})

	// (2) The bypass is worst under --allow-stale (older row, no --force): the
	//     guard must fire there too (it runs before the stale filter).
	t.Run("allow_stale_demote_reverted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "ids")
		epic, _ := seedEpicWithOpenChild(t, dir, "ids")

		// An OLDER updated_at: only --allow-stale imports it at all.
		const olderTS = "2000-01-01T00:00:00Z"
		jsonl := filepath.Join(t.TempDir(), "stale-demote.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"ids epic","issue_type":"task","updated_at":%q}`, epic.ID, olderTS))
		bdImport(t, bd, dir, jsonl, "--allow-stale")

		if got := bdShow(t, bd, dir, epic.ID); got.IssueType != types.TypeEpic {
			t.Errorf("beads-ts7vq: --allow-stale import silently demoted epic w/ open child to %q — must revert [BUG]", got.IssueType)
		}
		bdCloseFail(t, bd, dir, epic.ID) // close still refused (guard intact)
	})

	// (3) CONTROL / regression: an epic with NO open children may be demoted by
	//     import (matches the direct guard, which only fires on open children).
	//     The fix must not block a safe demote.
	t.Run("epic_no_open_children_demote_allowed", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "idn")
		epic, child := seedEpicWithOpenChild(t, dir, "idn")
		bdClose(t, bd, dir, child.ID) // close the child → no open children

		jsonl := filepath.Join(t.TempDir(), "safe-demote.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"idn epic","issue_type":"task","updated_at":%q}`, epic.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		if got := bdShow(t, bd, dir, epic.ID); got.IssueType != types.TypeTask {
			t.Errorf("beads-ts7vq: epic with no open children must be demotable by import, got %q", got.IssueType)
		}
	})

	// (4) CONTROL / regression: a NON-demote type change of an auto-closing
	//     parent (epic->molecule stays auto-closing) with open children is NOT a
	//     demote and must import normally — wouldRemainAutoClosingParent gates
	//     the target, so the guard must not fire on it.
	t.Run("epic_to_molecule_open_child_not_a_demote", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "idm")
		epic, _ := seedEpicWithOpenChild(t, dir, "idm")

		jsonl := filepath.Join(t.TempDir(), "promote.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"idm epic","issue_type":"molecule","updated_at":%q}`, epic.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		if got := bdShow(t, bd, dir, epic.ID); got.IssueType != types.TypeMolecule {
			t.Errorf("beads-ts7vq: epic->molecule (still auto-closing) is not a demote and must import, got %q", got.IssueType)
		}
	})

	// (5) CONTROL / regression: a genuinely-new epic in the import (no local row)
	//     must be created untouched — the guard only acts on updates over an
	//     existing auto-closing parent.
	t.Run("new_task_import_untouched", func(t *testing.T) {
		t.Parallel()
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "idc")
		_ = beadsDir
		jsonl := filepath.Join(t.TempDir(), "new.jsonl")
		writeRawJSONL(t, jsonl,
			`{"id":"idc-9001","title":"brand new task","issue_type":"task","updated_at":"2027-06-01T00:00:00Z"}`)
		bdImport(t, bd, dir, jsonl)
		if got := bdShow(t, bd, dir, "idc-9001"); got.IssueType != types.TypeTask {
			t.Errorf("beads-ts7vq: a genuinely-new task import must be untouched, got %q", got.IssueType)
		}
	})
}
