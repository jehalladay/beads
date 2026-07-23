//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedCreateClosedParentDoneCategory_ei6vq is the PROXIED twin of the
// beads-ei6vq create-axis teeth. A hub-connected (proxiedServerMode) crew runs
// `bd create --parent <id>` through create_proxied_server.go, whose closed-parent
// guard is a SEPARATE implementation from the direct create.go RunE. It keyed on
// the literal `parent.Status == types.StatusClosed`, so a proxied crew could mint
// an OPEN child under a done-category (terminal-but-not-closed) parent that a
// direct crew is blocked from — the read/guard-divergence class this repo keeps
// closing (6pjl6/j8ekq/a8a1b/50dto, and the ulsg4/u9lkx proxied twins).
//
// The fix resolves the done-set from the SAME check UOW before it is closed
// (doneCategoryStatusSetProxied) and swaps the literal test for
// parentStatusIsTerminal, at parity with the direct path.
//
// MUTATION-VERIFY: revert the proxied guard to a bare `== types.StatusClosed` →
// the done-category refuse leg goes RED (child created, rc=0) while the
// literal-closed control stays green.
func TestProxiedCreateClosedParentDoneCategory_ei6vq(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// (A) --parent under a done-category epic must be refused over the proxied
	//     path, exactly as a literally-closed parent is.
	t.Run("parent_flag_under_done_category_epic_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pea")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		epic := bdProxiedCreate(t, bd, p.dir, "Done epic", "-t", "epic")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "--status", "resolved") // childless → allowed
		out := bdProxiedCreateFail(t, bd, p.dir, "child under done epic", "--parent", epic.ID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("proxied `create --parent <done-category epic>` must be refused, got:\n%s", out)
		}
	})

	// (A2) --force overrides the proxied guard and lands the child.
	t.Run("parent_flag_under_done_category_epic_force_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pef")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		epic := bdProxiedCreate(t, bd, p.dir, "Done epic force", "-t", "epic")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "--status", "resolved")
		child := bdProxiedCreate(t, bd, p.dir, "forced child", "--parent", epic.ID, "--force")
		if child.ID == "" {
			t.Errorf("--force should land the child under a done-category epic (proxied), got empty id")
		}
	})

	// (B) NEGATIVE control: a literally-closed parent still guards the proxied
	//     create path — the done-category widen is additive, not a replacement.
	t.Run("literal_closed_epic_still_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "plc")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic", "-t", "epic")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "--status", "closed") // childless → allowed
		out := bdProxiedCreateFail(t, bd, p.dir, "child under closed epic", "--parent", epic.ID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("literal-closed parent must still guard the proxied create path, got:\n%s", out)
		}
	})

	// (C) NEGATIVE control: an OPEN epic parent allows the child over the proxied
	//     path (guard must not over-fire).
	t.Run("open_epic_parent_allowed", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "poa")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		epic := bdProxiedCreate(t, bd, p.dir, "Open epic", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "child under open epic", "--parent", epic.ID)
		if child.ID == "" {
			t.Errorf("child under an OPEN epic must land (proxied), got empty id")
		}
	})
}
