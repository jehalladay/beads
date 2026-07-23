//go:build cgo

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedCloseGuardParentStatusDoneCategory_ulsg4 is the PROXIED twin of the
// beads-ulsg4 teeth (see close_guard_parent_status_done_category_ulsg4_test.go):
// the hub-connected (proxiedServerMode) crew must enforce the SAME done-category
// parent-status terminality across the close-guard family as the direct path.
// The guards live in checkProxiedUpdateCloseGuards (update_proxied_server.go), a
// separate implementation from update.go's RunE, so a divergence here means a hub
// crew silently recreates the forbidden terminal-parent-with-open-child state that
// a direct crew is blocked from — the exact read/guard-divergence class this repo
// keeps closing (6pjl6/j8ekq/a8a1b/50dto).
//
// MUTATION-VERIFY: revert parentStatusIsTerminal to a bare
// `status == types.StatusClosed` → the forward + reparent done-category legs go
// RED (rc=0, forbidden state landed) while the literal-closed control stays green.
func TestProxiedCloseGuardParentStatusDoneCategory_ulsg4(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// (A) FORWARD UPDATE — the worst leg over the proxied path: moving an
	//     auto-closing parent to a custom done-category status while it has a
	//     genuinely open child must be refused, exactly as --status closed is.
	t.Run("forward_update_to_done_category_with_open_child_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pua")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		epic := bdProxiedCreate(t, bd, p.dir, "Epic", "-t", "epic")
		_ = bdProxiedCreate(t, bd, p.dir, "Open child", "--parent", epic.ID)
		out := bdProxiedUpdateFail(t, bd, p.dir, epic.ID, "--status", "resolved")
		if !strings.Contains(strings.ToLower(out), "open child") {
			t.Errorf("moving an auto-closing parent to a done-category status with an open child must be refused (proxied), got:\n%s", out)
		}
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, epic.ID); got == types.Status("resolved") {
			t.Errorf("epic must not reach the done-category terminal status with an open child (proxied), got %q", got)
		}
	})

	// (A2) FORWARD UPDATE positive: with the only child in a done-category status,
	//      moving the parent to a done-category status must SUCCEED.
	t.Run("forward_update_to_done_category_all_children_done_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pub")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		epic := bdProxiedCreate(t, bd, p.dir, "Epic", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Child", "--parent", epic.ID)
		bdProxiedUpdateOne(t, bd, p.dir, child.ID, "--status", "resolved")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "--status", "resolved")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, epic.ID); got != types.Status("resolved") {
			t.Errorf("epic should reach the done-category status with all-done children (proxied), got %q", got)
		}
	})

	// (B) REPARENT — reparenting an OPEN child under a done-category parent must be
	//     refused over the proxied path (parent-assignment axis), overridable with
	//     --force.
	t.Run("reparent_open_child_under_done_category_parent_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pra")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		epic := bdProxiedCreate(t, bd, p.dir, "Done epic", "-t", "epic")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "--status", "resolved") // childless → allowed
		child := bdProxiedCreate(t, bd, p.dir, "Loose task")
		out := bdProxiedUpdateFail(t, bd, p.dir, child.ID, "--parent", epic.ID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("reparenting an open child under a done-category parent must be refused (proxied), got:\n%s", out)
		}
		db := openProxiedDB(t, p)
		var count int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND depends_on_issue_id = ? AND type = 'parent-child'",
			child.ID, epic.ID).Scan(&count); err != nil {
			t.Fatalf("count parent dep: %v", err)
		}
		if count != 0 {
			t.Errorf("open child must not be reparented under a done-category parent (proxied), got %d parent edges", count)
		}
	})

	t.Run("reparent_open_child_under_done_category_parent_force_overrides", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "prf")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		epic := bdProxiedCreate(t, bd, p.dir, "Done epic force", "-t", "epic")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "--status", "resolved")
		child := bdProxiedCreate(t, bd, p.dir, "Loose task force")
		bdProxiedUpdateOne(t, bd, p.dir, child.ID, "--parent", epic.ID, "--force")
		db := openProxiedDB(t, p)
		assertProxiedDepExistsWithType(t, db, child.ID, epic.ID, string(types.DepParentChild))
	})

	// NEGATIVE (control): the literal-closed parent path is unchanged over the
	//     proxied path — the reparent guard still fires on a genuinely closed
	//     parent, proving the done-category widen is additive.
	t.Run("literal_closed_parent_still_guards_reparent", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "plc")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic", "-t", "epic")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "--status", "closed") // childless → allowed
		child := bdProxiedCreate(t, bd, p.dir, "Loose task")
		out := bdProxiedUpdateFail(t, bd, p.dir, child.ID, "--parent", epic.ID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("literal-closed parent must still guard reparent (proxied control), got:\n%s", out)
		}
	})
}
