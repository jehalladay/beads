//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedLintDoneCategoryEpicWithOpenChild is the beads-4nivb teeth. The
// closed-epic-with-open-children lint (beads-4u7d) had two legs: the CHILD leg
// (openChildIDsOfEpic) was made done-category-aware by beads-97gmg, but the
// EPIC/PARENT leg (scanClosedEpicsWithOpenChildren + closedEpicsToScan) still
// keyed on a LITERAL `status == StatusClosed`. So an epic parked in a custom
// done-category status (e.g. "verified") while it still has an open child — the
// SAME inconsistency the lint exists to catch — went undetected: it was neither
// treated as closed by the per-issue check NOR fetched into the no-args scan set.
//
// The fix treats a done-category epic the same as a literal-closed epic in both
// the per-issue check (epicCountsAsDone) and the scan-set fetch (also query each
// done-category status). Degraded-safe (empty done-set -> literal-closed only);
// FROZEN-category excluded (parked != done).
//
// Mutation: revert scanClosedEpicsWithOpenChildren's guard back to
// `issue.Status != types.StatusClosed` (drop epicCountsAsDone) → the two "flag"
// subtests go RED (the done-category epic is no longer detected), while the
// literal-closed and frozen negatives stay green.
func TestEmbeddedLintDoneCategoryEpicWithOpenChild(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ldc")

	// Register a done-category status ("verified") and a frozen (parked) one.
	bdConfig(t, bd, dir, "set", "status.custom", "verified:done,parked:frozen")

	// seedEpicOpenChild builds an epic + one open parent-child child, then moves
	// the epic to the given terminal-ish status via --force (the operator escape
	// the guard family permits), leaving the child open. Returns epic + child IDs.
	seedEpicOpenChild := func(t *testing.T, prefix, epicStatus string) (epic, child *types.Issue) {
		epic = bdCreate(t, bd, dir, prefix+" epic", "--type", "epic")
		child = bdCreate(t, bd, dir, prefix+" child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		if epicStatus == "closed" {
			bdClose(t, bd, dir, epic.ID, "--force")
		} else {
			// A done/frozen-category move is not blocked by the close guard, but
			// --force keeps the seed uniform against any future tightening.
			bdUpdate(t, bd, dir, epic.ID, "--status", epicStatus, "--force")
		}
		return epic, child
	}

	// (1) THE FIX: a DONE-CATEGORY epic ("verified") with an open child is flagged
	//     as a closed_epic_with_open_children inconsistency (JSON path).
	t.Run("json_flags_done_category_epic_with_open_child", func(t *testing.T) {
		epic, child := seedEpicOpenChild(t, "jd", "verified")
		m := bdLintJSON(t, bd, dir)
		incRaw, ok := m["inconsistencies"].([]interface{})
		if !ok || len(incRaw) == 0 {
			t.Fatalf("expected inconsistency for done-category epic %s with open child %s, got: %v",
				epic.ID, child.ID, m["inconsistencies"])
		}
		found := false
		for _, r := range incRaw {
			rm := r.(map[string]interface{})
			if rm["id"] != epic.ID {
				continue
			}
			if rm["kind"] != "closed_epic_with_open_children" {
				t.Errorf("expected kind closed_epic_with_open_children, got %v", rm["kind"])
			}
			oc, _ := rm["open_children"].([]interface{})
			childSeen := false
			for _, c := range oc {
				if c == child.ID {
					childSeen = true
				}
			}
			if !childSeen {
				t.Errorf("expected open child %s listed for done-category epic %s, got %v", child.ID, epic.ID, oc)
			}
			found = true
		}
		if !found {
			t.Errorf("done-category epic %s not in inconsistencies", epic.ID)
		}
	})

	// (2) THE FIX (human-readable + exit contract): the same done-category epic is
	//     surfaced in the text scan and the command exits non-zero.
	t.Run("human_readable_flags_done_category_epic", func(t *testing.T) {
		epic, _ := seedEpicOpenChild(t, "hd", "verified")
		out, exitCode := bdLint(t, bd, dir)
		if !strings.Contains(out, "Structural inconsistencies") {
			t.Errorf("expected 'Structural inconsistencies' section, got:\n%s", out)
		}
		if !strings.Contains(out, epic.ID) || !strings.Contains(out, "closed epic with") {
			t.Errorf("expected closed-epic-with-open-child line for done-category epic %s, got:\n%s", epic.ID, out)
		}
		if exitCode != 1 {
			t.Errorf("expected exit code 1 when a done-category inconsistency exists, got %d", exitCode)
		}
	})

	// (3) THE FIX (explicit arg-scoped path): linting the done-category epic ID
	//     directly also flags it.
	t.Run("explicit_done_category_epic_id_flags", func(t *testing.T) {
		epic, _ := seedEpicOpenChild(t, "ed", "verified")
		m := bdLintJSON(t, bd, dir, epic.ID)
		incRaw, ok := m["inconsistencies"].([]interface{})
		if !ok || len(incRaw) == 0 {
			t.Fatalf("expected inconsistency when linting done-category epic %s explicitly", epic.ID)
		}
	})

	// (4) NEGATIVE (regression): a literal-CLOSED epic with an open child is still
	//     flagged (the pre-4nivb behavior must be byte-identical).
	t.Run("literal_closed_epic_still_flagged", func(t *testing.T) {
		epic, _ := seedEpicOpenChild(t, "lc", "closed")
		m := bdLintJSON(t, bd, dir, epic.ID)
		incRaw, ok := m["inconsistencies"].([]interface{})
		if !ok || len(incRaw) == 0 {
			t.Fatalf("expected literal-closed epic %s to still be flagged", epic.ID)
		}
	})

	// (5) NEGATIVE (scope): a FROZEN-category epic ("parked") is NOT done — it must
	//     NOT be flagged (parked != complete; matches x463g/97gmg/ulsg4 Done-only
	//     semantics). This is the guard against over-widening the detector.
	t.Run("frozen_category_epic_not_flagged", func(t *testing.T) {
		epic, _ := seedEpicOpenChild(t, "fr", "parked")
		m := bdLintJSON(t, bd, dir, epic.ID)
		if incRaw, ok := m["inconsistencies"].([]interface{}); ok {
			for _, r := range incRaw {
				if r.(map[string]interface{})["id"] == epic.ID {
					t.Errorf("frozen (parked) epic %s must not be flagged as a closed-epic inconsistency", epic.ID)
				}
			}
		}
	})

	// (6) NEGATIVE (regression): a done-category epic whose children are ALL closed
	//     is consistent — no flag (the child leg already excludes done/closed).
	t.Run("done_category_epic_all_children_closed_clean", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "dcc epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "dcc child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)
		bdUpdate(t, bd, dir, epic.ID, "--status", "verified", "--force")
		m := bdLintJSON(t, bd, dir, epic.ID)
		if incRaw, ok := m["inconsistencies"].([]interface{}); ok {
			for _, r := range incRaw {
				if r.(map[string]interface{})["id"] == epic.ID {
					t.Errorf("consistent done-category epic %s (all children closed) should not be flagged", epic.ID)
				}
			}
		}
	})
}
