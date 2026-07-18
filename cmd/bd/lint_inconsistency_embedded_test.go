//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedLintClosedEpicWithOpenChild verifies the beads-4u7d fix: `bd lint`
// FLAGS any closed epic that still has one or more open parent-child children —
// the "a closed epic has no open children" invariant violation — regardless of how
// the state was reached. This is the defensive-lint axis of the close-guard family:
// the guards (beads-2hkd demote, beads-b0tw child-reopen, beads-eth8 dep-add,
// epic-close in close.go) PREVENT the state going forward, but the state can still
// exist via a --force override, a not-yet-guarded mutation path, or a pre-existing
// row from before the guards landed. The seed here reaches it deliberately with
// `bd close <epic> --force` (the operator escape hatch the guard family allows).
//
// RED/GREEN: neutralize scanClosedEpicsWithOpenChildren (return nil) and only the
// two "flag" subtests (verb + json) fail; the clean/negative subtests stay green.
func TestEmbeddedLintClosedEpicWithOpenChild(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "li")

	// seedClosedEpicOpenChild builds the inconsistent state: an epic with one open
	// parent-child child, with the epic force-closed (child left open). Returns the
	// epic and child IDs.
	seedClosedEpicOpenChild := func(t *testing.T, prefix string) (epic, child *types.Issue) {
		epic = bdCreate(t, bd, dir, prefix+" epic", "--type", "epic")
		child = bdCreate(t, bd, dir, prefix+" child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		// Force-close the epic while the child is open — the guard family permits
		// this operator override, so it is exactly the state the lint must flag.
		bdClose(t, bd, dir, epic.ID, "--force")
		return epic, child
	}

	// The default lint scan (JSON) must surface the closed epic as an inconsistency
	// even though a closed epic is invisible to the default --status=open template
	// scan — beads-4u7d scans closed epics independently.
	t.Run("json_flags_closed_epic_with_open_child", func(t *testing.T) {
		epic, child := seedClosedEpicOpenChild(t, "jf")
		m := bdLintJSON(t, bd, dir)
		incRaw, ok := m["inconsistencies"].([]interface{})
		if !ok || len(incRaw) == 0 {
			t.Fatalf("expected inconsistencies for closed epic %s with open child %s, got: %v",
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
				t.Errorf("expected open child %s listed for epic %s, got %v", child.ID, epic.ID, oc)
			}
			found = true
		}
		if !found {
			t.Errorf("closed epic %s not in inconsistencies", epic.ID)
		}
	})

	// The human-readable scan must also surface it (and exit non-zero).
	t.Run("human_readable_flags_closed_epic_with_open_child", func(t *testing.T) {
		epic, _ := seedClosedEpicOpenChild(t, "hf")
		out, exitCode := bdLint(t, bd, dir)
		if !strings.Contains(out, "Structural inconsistencies") {
			t.Errorf("expected 'Structural inconsistencies' section, got:\n%s", out)
		}
		if !strings.Contains(out, epic.ID) || !strings.Contains(out, "closed epic with") {
			t.Errorf("expected closed-epic-with-open-child line for %s, got:\n%s", epic.ID, out)
		}
		if exitCode != 1 {
			t.Errorf("expected exit code 1 when an inconsistency exists, got %d", exitCode)
		}
	})

	// Linting the epic ID explicitly also flags it (arg-scoped path).
	t.Run("explicit_epic_id_flags_inconsistency", func(t *testing.T) {
		epic, _ := seedClosedEpicOpenChild(t, "ef")
		m := bdLintJSON(t, bd, dir, epic.ID)
		incRaw, ok := m["inconsistencies"].([]interface{})
		if !ok || len(incRaw) == 0 {
			t.Fatalf("expected inconsistency when linting epic %s explicitly", epic.ID)
		}
	})

	// NEGATIVE: a closed epic whose children are ALL closed is consistent — no flag.
	t.Run("closed_epic_all_children_closed_clean", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "cc epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "cc child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)
		bdClose(t, bd, dir, epic.ID) // no --force needed; child already closed
		m := bdLintJSON(t, bd, dir, epic.ID)
		if incRaw, ok := m["inconsistencies"].([]interface{}); ok {
			for _, r := range incRaw {
				if r.(map[string]interface{})["id"] == epic.ID {
					t.Errorf("consistent closed epic %s should not be flagged", epic.ID)
				}
			}
		}
	})

	// NEGATIVE: an OPEN epic with an open child is fine (invariant only applies to
	// CLOSED epics) — no flag.
	t.Run("open_epic_open_child_clean", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "oe epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "oe child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		m := bdLintJSON(t, bd, dir, epic.ID)
		if incRaw, ok := m["inconsistencies"].([]interface{}); ok {
			for _, r := range incRaw {
				if r.(map[string]interface{})["id"] == epic.ID {
					t.Errorf("open epic %s should not be flagged", epic.ID)
				}
			}
		}
	})

	// NEGATIVE: a closed NON-epic (task) with an open "child"-style edge is not an
	// epic-invariant violation — the scan is epic-scoped, so no flag.
	t.Run("closed_nonepic_not_flagged", func(t *testing.T) {
		parent := bdCreate(t, bd, dir, "np parent", "--type", "task")
		child := bdCreate(t, bd, dir, "np child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, parent.ID, "--type", "parent-child")
		bdClose(t, bd, dir, parent.ID, "--force")
		m := bdLintJSON(t, bd, dir, parent.ID)
		if incRaw, ok := m["inconsistencies"].([]interface{}); ok {
			for _, r := range incRaw {
				if r.(map[string]interface{})["id"] == parent.ID {
					t.Errorf("closed non-epic %s should not be flagged as an epic-invariant violation", parent.ID)
				}
			}
		}
	})
}
