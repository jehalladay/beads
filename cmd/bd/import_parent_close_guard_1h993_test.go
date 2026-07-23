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

// TestEmbeddedImportParentCloseGuard_1h993 is the beads-1h993 teeth: axis B of
// the close-guard family's IMPORT bypass (beads-ts7vq). The auto-closing-parent
// close guard (countEpicOpenChildren, cmd/bd/close.go) refuses closing an
// epic/molecule/wisp root with open children on BOTH `bd close` and `bd update
// --status closed` (update.go, beads-zgku). But `bd import` applies the incoming
// status field-wise through its upsert with NO such check — so an import row
// that flips an auto-closing parent (with open children) to CLOSED silently
// plants the forbidden closed-parent-with-open-child state. This is the WORST
// axis of the family: it needs NO flag at all (closed-status is the ordinary
// payload of any export/import round-trip), unlike the type-demote axis (ts7vq).
//
// The fix (guardImportParentClose in import_shared.go) REVERTS the incoming
// status to the local value for the offending rows — import's skip-and-report /
// preserve-on-absent model, not an all-or-nothing abort — and reports the ids so
// the (otherwise silent) bypass is visible. All other fields on the row still
// import.
//
// Driven END-TO-END through the real `bd import` subprocess so the teeth
// exercise the actual upsert + guard plumbing (a core-func unit test would miss
// the CLI wiring). MUTATION-VERIFIED: neuter guardImportParentClose (e.g.
// `return nil, nil` at its top, or drop the `issue.Status = local.Status`
// revert) → epic_with_open_child_close_reverted + allow_stale_close_reverted go
// RED (status closes and the parent ends up closed with an open child).
func TestEmbeddedImportParentCloseGuard_1h993(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A far-future updated_at makes the import line strictly newer than the
	// just-created local row, so the default stale guard never skips it and the
	// upsert (the thing under test) actually runs on the guarded path.
	const newerTS = "2027-06-01T00:00:00Z"

	// seedEpicWithOpenChild creates an OPEN auto-closing parent + an OPEN
	// parent-child child. The direct `bd close <epic>` / `bd update <epic>
	// --status closed` is refused while the child is open.
	seedEpicWithOpenChild := func(t *testing.T, dir, prefix, parentType string) (parent, child *types.Issue) {
		t.Helper()
		parent = bdCreate(t, bd, dir, prefix+" parent", "--type", parentType)
		child = bdCreate(t, bd, dir, prefix+" child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, parent.ID, "--type", "parent-child")
		return parent, child
	}

	// (1) Import row that CLOSES an epic-with-open-child is REVERTED: the local
	//     status stays open, the row is reported, and the parent does not end up
	//     closed with an open child. This is the exact 1h993 repro through
	//     `bd import`, and it needs NO flag.
	t.Run("epic_with_open_child_close_reverted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "icg")
		epic, _ := seedEpicWithOpenChild(t, dir, "icg", "epic")

		// Sanity: the DIRECT close is refused (the guard we mirror works).
		out := bdCloseFail(t, bd, dir, epic.ID)
		if !strings.Contains(out, "open child") && !strings.Contains(out, "--force") {
			t.Fatalf("precondition: direct `bd close` on epic w/ open child must be refused; got:\n%s", out)
		}

		// IMPORT the same close (newer-ts line flipping status open->closed).
		jsonl := filepath.Join(t.TempDir(), "close.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"icg parent","issue_type":"epic","status":"closed","updated_at":%q}`, epic.ID, newerTS))
		importOut := bdImport(t, bd, dir, jsonl)

		// The status must NOT have closed — the guard reverted it to open.
		if got := bdShow(t, bd, dir, epic.ID); got.Status == types.StatusClosed {
			t.Errorf("beads-1h993: import silently closed epic w/ open child — must revert to open [BUG]")
		}
		// The revert must be reported (not silent).
		if !strings.Contains(importOut, epic.ID) || !strings.Contains(importOut, "Kept parent status") {
			t.Errorf("beads-1h993: import close-revert must report the id %s; got:\n%s", epic.ID, importOut)
		}
	})

	// (2) The bypass also works under --allow-stale (older row, no --force): the
	//     guard must fire there too (it runs before the stale filter).
	t.Run("allow_stale_close_reverted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "ics")
		epic, _ := seedEpicWithOpenChild(t, dir, "ics", "epic")

		const olderTS = "2000-01-01T00:00:00Z"
		jsonl := filepath.Join(t.TempDir(), "stale-close.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"ics parent","issue_type":"epic","status":"closed","updated_at":%q}`, epic.ID, olderTS))
		bdImport(t, bd, dir, jsonl, "--allow-stale")

		if got := bdShow(t, bd, dir, epic.ID); got.Status == types.StatusClosed {
			t.Errorf("beads-1h993: --allow-stale import silently closed epic w/ open child — must revert [BUG]")
		}
	})

	// (3) The guard is not epic-specific: a molecule root (auto-closing) with an
	//     open child is guarded the same way.
	t.Run("molecule_with_open_child_close_reverted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "icm")
		mol, _ := seedEpicWithOpenChild(t, dir, "icm", "molecule")

		jsonl := filepath.Join(t.TempDir(), "mol-close.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"icm parent","issue_type":"molecule","status":"closed","updated_at":%q}`, mol.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		if got := bdShow(t, bd, dir, mol.ID); got.Status == types.StatusClosed {
			t.Errorf("beads-1h993: import silently closed molecule root w/ open child — must revert [BUG]")
		}
	})

	// (4) CONTROL / regression: an epic with NO open children may be closed by
	//     import (matches the direct guard, which only fires on open children).
	//     The fix must not block a safe close.
	t.Run("epic_no_open_children_close_allowed", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "icn")
		epic, child := seedEpicWithOpenChild(t, dir, "icn", "epic")
		bdClose(t, bd, dir, child.ID) // close the child → no open children

		jsonl := filepath.Join(t.TempDir(), "safe-close.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"icn parent","issue_type":"epic","status":"closed","updated_at":%q}`, epic.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		if got := bdShow(t, bd, dir, epic.ID); got.Status != types.StatusClosed {
			t.Errorf("beads-1h993: epic with no open children must be closable by import, got status %q", got.Status)
		}
	})

	// (5) CONTROL / regression: a NON-auto-closing parent (a plain task with a
	//     parent-child child) is NOT subject to the close guard — closing it via
	//     import must succeed (the direct guard only fires on auto-closing types).
	t.Run("non_autoclosing_parent_close_allowed", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "ict")
		parent := bdCreate(t, bd, dir, "ict parent", "--type", "task")
		child := bdCreate(t, bd, dir, "ict child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, parent.ID, "--type", "parent-child")

		jsonl := filepath.Join(t.TempDir(), "task-close.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"ict parent","issue_type":"task","status":"closed","updated_at":%q}`, parent.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		if got := bdShow(t, bd, dir, parent.ID); got.Status != types.StatusClosed {
			t.Errorf("beads-1h993: a non-auto-closing (task) parent must be closable by import, got status %q", got.Status)
		}
	})

	// (6) CONTROL / regression: a genuinely-new closed epic in the import (no
	//     local row) must be created untouched — the guard only acts on updates
	//     over an existing auto-closing parent (a fresh epic has no committed
	//     children yet).
	t.Run("new_closed_epic_import_untouched", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "icc")
		jsonl := filepath.Join(t.TempDir(), "new-closed.jsonl")
		writeRawJSONL(t, jsonl,
			`{"id":"icc-9001","title":"brand new closed epic","issue_type":"epic","status":"closed","updated_at":"2027-06-01T00:00:00Z"}`)
		bdImport(t, bd, dir, jsonl)
		if got := bdShow(t, bd, dir, "icc-9001"); got.Status != types.StatusClosed {
			t.Errorf("beads-1h993: a genuinely-new closed epic import must be untouched, got status %q", got.Status)
		}
	})
}
