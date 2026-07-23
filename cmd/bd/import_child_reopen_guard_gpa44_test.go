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

// TestEmbeddedImportChildReopenGuard_gpa44 is the beads-gpa44 teeth: axis C of
// the close-guard family's IMPORT bypass (beads-ts7vq). The child-reopen guard
// (closedEpicParents, cmd/bd/reopen.go / update.go beads-b0tw) refuses reopening
// a closed child whose auto-closing parent is itself still closed — that reopen
// recreates the forbidden closed-parent-with-open-child state. But `bd import`
// applies the incoming status field-wise through its upsert with NO such check,
// so an import row that flips a child closed → open under a still-closed parent
// silently plants that state.
//
// The fix (guardImportChildReopen in import_shared.go) REVERTS the incoming
// status to the local value for the offending rows — import's skip-and-report /
// preserve-on-absent model, not an all-or-nothing abort — and reports the ids so
// the (otherwise silent) bypass is visible. All other fields on the row still
// import.
//
// Driven END-TO-END through the real `bd import` subprocess so the teeth
// exercise the actual upsert + guard plumbing. MUTATION-VERIFIED: neuter
// guardImportChildReopen (e.g. `return nil, nil` at its top, or drop the
// `issue.Status = local.Status` revert) → child_reopen_under_closed_parent_
// reverted + allow_stale_child_reopen_reverted go RED (the child reopens and
// the closed-parent-with-open-child state is planted).
func TestEmbeddedImportChildReopenGuard_gpa44(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A far-future updated_at makes the import line strictly newer than the
	// just-created local row, so the default stale guard never skips it and the
	// upsert (the thing under test) actually runs on the guarded path.
	const newerTS = "2027-06-01T00:00:00Z"

	// seedClosedParentClosedChild creates an auto-closing parent + a
	// parent-child child, then closes the child and the parent — reaching a
	// legitimately CLOSED parent with a CLOSED child. Reopening the child (the
	// thing under test) is what the direct guard refuses. Returns both.
	seedClosedParentClosedChild := func(t *testing.T, dir, prefix, parentType string) (parent, child *types.Issue) {
		t.Helper()
		parent = bdCreate(t, bd, dir, prefix+" parent", "--type", parentType)
		child = bdCreate(t, bd, dir, prefix+" child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, parent.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)  // child closed first (no open children left)
		bdClose(t, bd, dir, parent.ID) // parent now closable
		return parent, child
	}

	// (1) Import row that REOPENS a child under a still-closed parent is
	//     REVERTED: the local status stays closed, the row is reported, and the
	//     forbidden closed-parent-with-open-child state is not planted. This is
	//     the exact gpa44 repro through `bd import`.
	t.Run("child_reopen_under_closed_parent_reverted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "irg")
		_, child := seedClosedParentClosedChild(t, dir, "irg", "epic")

		// Sanity: the DIRECT reopen is refused (the guard we mirror works).
		out := bdUpdateFail(t, bd, dir, child.ID, "--status", "open")
		if !strings.Contains(out, "closed") && !strings.Contains(out, "--force") {
			t.Fatalf("precondition: direct `bd update --status open` on a child under a closed parent must be refused; got:\n%s", out)
		}

		// IMPORT the same reopen (newer-ts line flipping status closed->open,
		// with closed_at:null as a genuine `bd export` of a reopened issue emits
		// — this is the realistic bypass payload and also proves the guard
		// reverts the coupled status+closed_at pair, not status alone).
		jsonl := filepath.Join(t.TempDir(), "reopen.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"irg child","issue_type":"task","status":"open","closed_at":null,"updated_at":%q}`, child.ID, newerTS))
		importOut := bdImport(t, bd, dir, jsonl)

		// The status must NOT have reopened — the guard reverted it to closed.
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusClosed {
			t.Errorf("beads-gpa44: import silently reopened a child under a closed parent (status=%q) — must revert to closed [BUG]", got.Status)
		}
		// The revert must be reported (not silent).
		if !strings.Contains(importOut, child.ID) || !strings.Contains(importOut, "Kept child status") {
			t.Errorf("beads-gpa44: import child-reopen-revert must report the id %s; got:\n%s", child.ID, importOut)
		}
	})

	// (2) The bypass also works under --allow-stale (older row, no --force): the
	//     guard must fire there too (it runs before the stale filter).
	t.Run("allow_stale_child_reopen_reverted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "irs")
		_, child := seedClosedParentClosedChild(t, dir, "irs", "epic")

		const olderTS = "2000-01-01T00:00:00Z"
		jsonl := filepath.Join(t.TempDir(), "stale-reopen.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"irs child","issue_type":"task","status":"open","closed_at":null,"updated_at":%q}`, child.ID, olderTS))
		bdImport(t, bd, dir, jsonl, "--allow-stale")

		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusClosed {
			t.Errorf("beads-gpa44: --allow-stale import silently reopened a child under a closed parent (status=%q) — must revert [BUG]", got.Status)
		}
	})

	// (3) CONTROL / regression: reopening a child whose parent is OPEN is safe —
	//     the direct guard only fires when the parent is closed. The fix must not
	//     block a legitimate reopen.
	t.Run("child_reopen_under_open_parent_allowed", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "iro")
		parent := bdCreate(t, bd, dir, "iro parent", "--type", "epic")
		child := bdCreate(t, bd, dir, "iro child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, parent.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID) // child closed, parent left OPEN

		// A genuine reopen line clears closed_at (what `bd export` emits for a
		// reopened issue); status=open with a lingering closed_at violates the
		// closed<=>closed_at invariant. Emit closed_at:null explicitly so this
		// exercises the guard's allow-path, not the closed_at validation.
		jsonl := filepath.Join(t.TempDir(), "safe-reopen.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"iro child","issue_type":"task","status":"open","closed_at":null,"updated_at":%q}`, child.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("beads-gpa44: a child under an OPEN parent must be reopenable by import, got status %q", got.Status)
		}
	})

	// (4) CONTROL / regression: an already-open child re-imported open under a
	//     closed parent is a no-op (no closed->open transition) and must not be
	//     spuriously reverted or reported.
	t.Run("open_child_reimport_not_reverted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "iru")
		// A closed parent with an open child already exists only via --force;
		// simpler: an orphan open child (no closed parent) re-imported open is a
		// clean no-op transition — exercises the "not a closed->open" branch.
		child := bdCreate(t, bd, dir, "iru child", "--type", "task")

		jsonl := filepath.Join(t.TempDir(), "noop.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"iru child","issue_type":"task","status":"open","updated_at":%q}`, child.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl)

		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("beads-gpa44: an open->open re-import must stay open, got %q", got.Status)
		}
		if strings.Contains(out, "Kept child status") {
			t.Errorf("beads-gpa44: an open->open re-import must NOT be reported as a child-reopen revert; got:\n%s", out)
		}
	})

	// (5) CONTROL / regression: a genuinely-new open child in the import (no
	//     local row) must be created untouched — the guard only acts on updates
	//     over an existing local child.
	t.Run("new_open_child_import_untouched", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "irc")
		jsonl := filepath.Join(t.TempDir(), "new.jsonl")
		writeRawJSONL(t, jsonl,
			`{"id":"irc-9001","title":"brand new open child","issue_type":"task","status":"open","updated_at":"2027-06-01T00:00:00Z"}`)
		bdImport(t, bd, dir, jsonl)
		if got := bdShow(t, bd, dir, "irc-9001"); got.Status != types.StatusOpen {
			t.Errorf("beads-gpa44: a genuinely-new open child import must be untouched, got status %q", got.Status)
		}
	})
}
