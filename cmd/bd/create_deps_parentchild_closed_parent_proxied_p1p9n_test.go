//go:build cgo

package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedCreateDepsParentChildClosedParentGuard is the beads-p1p9n teeth for
// the PROXIED (hub-connected) create path. The closed-parent guard family refuses
// an OPEN child under a CLOSED auto-closing parent (epic/molecule/wisp) on the
// `--parent` axis (create_proxied_server.go single guard) and the graph axis
// (t39ph). But `bd create --deps parent-child:<closed-parent>` routes the edge
// through the GENERIC deps loop into domain create()'s Dependencies pass, which
// had NO closed-parent guard — so a hub-connected crew could smuggle an open
// child under a closed epic/molecule via --deps (rc=0, forbidden state recreated).
//
// This is the proxied twin of the embedded/direct test
// (TestEmbeddedCreateDepsParentChildClosedParentGuard) and the create-side sibling
// of j8ekq (proxied dep-add). Fix: domain create() guards the parent-child
// dep-spec seam, honoring params.Force (threaded from --force). Mutation: remove
// the create()-loop guard → the refuse cases below go rc!=0 → rc=0 (RED).
func TestProxiedCreateDepsParentChildClosedParentGuard(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// (1) --deps parent-child:<closed EPIC> is refused on the proxied path — the
	//     same guard the single `create --parent <closed-epic>` proxied path
	//     enforces, now on the generic-dep axis too.
	t.Run("deps_parent_child_under_closed_epic_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pde1")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic deps", "-t", "epic")
		bdProxiedClose(t, bd, p.dir, epic.ID) // childless epic closes clean
		out := bdProxiedCreateFail(t, bd, p.dir, "deps child epic", "--deps", "parent-child:"+epic.ID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on proxied create --deps parent-child under closed epic, got:\n%s", out)
		}
	})

	// (2) MOLECULE parent (aw9x8 widening axis): a closed molecule root must be
	//     guarded on this axis too, not just epics.
	t.Run("deps_parent_child_under_closed_molecule_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pdm1")
		mol := bdProxiedCreate(t, bd, p.dir, "Closed molecule deps", "-t", "molecule")
		bdProxiedClose(t, bd, p.dir, mol.ID)
		out := bdProxiedCreateFail(t, bd, p.dir, "deps child mol", "--deps", "parent-child:"+mol.ID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on proxied create --deps parent-child under closed molecule, got:\n%s", out)
		}
	})

	// (3) --force overrides the guard and lands the child + edge.
	t.Run("deps_parent_child_under_closed_epic_force_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pdf1")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic deps force", "-t", "epic")
		bdProxiedClose(t, bd, p.dir, epic.ID)
		child := bdProxiedCreate(t, bd, p.dir, "forced deps child", "--deps", "parent-child:"+epic.ID, "--force")
		if child.ID == "" {
			t.Errorf("--force should land the child under a closed epic via --deps, got empty id")
		}
	})

	// (4) OPEN epic parent (regression control): a child under an OPEN parent via
	//     --deps must still succeed — the guard fires only on a CLOSED parent.
	t.Run("deps_parent_child_under_open_epic_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pdo1")
		epic := bdProxiedCreate(t, bd, p.dir, "Open epic deps", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "deps child open", "--deps", "parent-child:"+epic.ID)
		if child.ID == "" {
			t.Errorf("child under an OPEN epic via --deps should land, got empty id")
		}
		// The child stays OPEN and its create succeeded — the guard did NOT fire on
		// an open parent. (A parent-child edge does not set is_blocked, so blocking
		// state is not the signal here; a clean create IS the regression control.)
		if got := readStatus(t, openProxiedDB(t, p), child.ID); got != types.StatusOpen {
			t.Errorf("child under an open epic must stay open, got %q", got)
		}
	})

	// (5) a non-parent-child edge (blocks) to a CLOSED issue must NOT trip the
	//     closed-PARENT guard — proves the guard is scoped to parent-child. The
	//     blocks target is a plain closed task (not an epic) so the unrelated
	//     "epics can only block epics" type rule doesn't interfere.
	t.Run("deps_blocks_to_closed_issue_unaffected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pdb1")
		blocker := bdProxiedCreate(t, bd, p.dir, "Closed blocker task")
		bdProxiedClose(t, bd, p.dir, blocker.ID)
		child := bdProxiedCreate(t, bd, p.dir, "deps blocks child", "--deps", "blocks:"+blocker.ID)
		if child.ID == "" {
			t.Errorf("a blocks: edge to a closed issue must not trip the closed-parent guard, got empty id")
		}
		if got := readStatus(t, openProxiedDB(t, p), child.ID); got != types.StatusOpen {
			t.Errorf("child must stay open, got %q", got)
		}
	})
}
