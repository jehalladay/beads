//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedGraphCreateClosedParentGuard is the beads-t39ph teeth: the
// create-axis closed-parent guard (create.go, beads-a8a1b/czu1s) is gated on
// parentID != "", but `bd create --graph` mints every node TOP-LEVEL (Pass 1,
// no ParentID) and links parent-child in a post-pass — so the create-time guard
// never ran for graph children, and a graph child under a CLOSED auto-closing
// parent (epic OR molecule OR wisp, beads-aw9x8) landed silently (rc=0). Same
// top-level-then-link seam that made label inheritance (beads-l8qsn) and
// assignee-normalize (beads-7i4m/llzt) diverge on the graph paths.
//
// Fix mirrors the guard into BOTH graph parent-child link passes (embedded
// executeGraphApply + domain applyGraph) using the shared
// types.Issue.IsAutoClosingParentType, honoring --force. Mutation: removing the
// guard from executeGraphApply turns the refuse cases below RED (child minted,
// rc=0).
func TestEmbeddedGraphCreateClosedParentGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	writePlan := func(t *testing.T, dir, name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write plan %s: %v", name, err)
		}
		return p
	}

	makeClosedParent := func(t *testing.T, dir, title, typ string) string {
		t.Helper()
		p := bdCreate(t, bd, dir, title, "--type", typ)
		bdClose(t, bd, dir, p.ID)
		if got := bdShow(t, bd, dir, p.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: %s %s should be closed, got %s", typ, p.ID, got.Status)
		}
		return p.ID
	}

	// graphCreateFail runs `bd create --graph <plan>` expecting a non-zero exit,
	// returning combined output for assertion.
	graphCreateFail := func(t *testing.T, dir, plan string, extra ...string) string {
		t.Helper()
		planFile := writePlan(t, dir, "plan.json", plan)
		args := append([]string{"create", "--graph", planFile}, extra...)
		out, err := bdRunWithFlockRetry(t, bd, dir, args...)
		if err == nil {
			t.Fatalf("bd create --graph should have failed, got success:\n%s", out)
		}
		return string(out)
	}

	// (1) graph child with parent_id to a CLOSED EPIC is refused — the guard the
	//     single `create --parent <closed-epic>` path already enforced, now on
	//     the --graph path too.
	t.Run("graph_child_under_closed_epic_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gce")
		epic := makeClosedParent(t, dir, "closed epic graph", "epic")
		plan := `{"nodes":[{"key":"c","title":"graph child","type":"task","parent_id":"` + epic + `"}],"edges":[]}`
		out := graphCreateFail(t, dir, plan)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on graph create under closed epic, got:\n%s", out)
		}
	})

	// (2) MOLECULE parent (aw9x8 widening axis) — a closed molecule root must be
	//     guarded on the graph path too, not just epics.
	t.Run("graph_child_under_closed_molecule_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gcm")
		mol := makeClosedParent(t, dir, "closed molecule graph", "molecule")
		plan := `{"nodes":[{"key":"c","title":"graph mol child","type":"task","parent_id":"` + mol + `"}],"edges":[]}`
		out := graphCreateFail(t, dir, plan)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on graph create under closed molecule, got:\n%s", out)
		}
	})

	// (3) --force overrides the graph guard and lands the child.
	t.Run("graph_child_under_closed_epic_force_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gcf")
		epic := makeClosedParent(t, dir, "closed epic graph force", "epic")
		plan := `{"nodes":[{"key":"c","title":"forced graph child","type":"task","parent_id":"` + epic + `"}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "pf.json", plan), "--force")
		if res.IDs["c"] == "" {
			t.Errorf("--force should land the graph child under a closed epic, got: %v", res.IDs)
		}
	})

	// (4) OPEN epic parent (regression control): a graph child under an OPEN
	//     parent must still succeed — the guard fires only on a CLOSED parent.
	t.Run("graph_child_under_open_epic_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gco")
		epic := bdCreate(t, bd, dir, "open epic graph", "--type", "epic")
		plan := `{"nodes":[{"key":"c","title":"child under open epic","type":"task","parent_id":"` + epic.ID + `"}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "po.json", plan))
		if res.IDs["c"] == "" {
			t.Errorf("graph child under an OPEN epic should land, got: %v", res.IDs)
		}
	})

	// (5) parent_key to a CLOSED parent declared IN-PLAN is refused too — proves
	//     the guard runs in the post-pass after keyToID resolves, not just for
	//     pre-existing parent_id. (Create an already-closed parent, reference it
	//     by parent_id; the parent_key case for an in-plan CLOSED parent can't
	//     arise since graph nodes are minted open — so this covers the realistic
	//     closed-parent shape: an existing closed root referenced by id.)
	t.Run("graph_open_parent_in_plan_still_links", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gck")
		// parent and child both in-plan (parent open) — must succeed and link.
		plan := `{"nodes":[{"key":"p","title":"in-plan parent","type":"epic"},{"key":"c","title":"in-plan child","type":"task","parent_key":"p"}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "pk.json", plan))
		if res.IDs["p"] == "" || res.IDs["c"] == "" {
			t.Errorf("in-plan open parent+child should both mint and link, got: %v", res.IDs)
		}
	})

	// (6) EDGE-LOOP SEAM (dogfooder cascade-coverage): a parent-child link
	//     declared as an explicit EDGE — {from:child, to:closed-parent,
	//     type:"parent-child"} — with NO node.ParentID is added in the Pass-3
	//     edge loop, a THIRD seam distinct from the two node.ParentID post-passes
	//     the guard above targets. Before this leg, an open child linked under a
	//     closed epic/molecule via an explicit edge landed rc=0. Mutation:
	//     removing the edge-loop guard turns these RED. The parent is a pre-
	//     existing closed root referenced by to_id.
	t.Run("edge_parent_child_under_closed_epic_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gee")
		epic := makeClosedParent(t, dir, "closed epic edge", "epic")
		plan := `{"nodes":[{"key":"c","title":"edge child","type":"task"}],"edges":[{"from_key":"c","to_id":"` + epic + `","type":"parent-child"}]}`
		out := graphCreateFail(t, dir, plan)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on explicit parent-child EDGE under closed epic, got:\n%s", out)
		}
	})

	t.Run("edge_parent_child_under_closed_molecule_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gem")
		mol := makeClosedParent(t, dir, "closed molecule edge", "molecule")
		plan := `{"nodes":[{"key":"c","title":"edge mol child","type":"task"}],"edges":[{"from_key":"c","to_id":"` + mol + `","type":"parent-child"}]}`
		out := graphCreateFail(t, dir, plan)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent guard on explicit parent-child EDGE under closed molecule, got:\n%s", out)
		}
	})

	// (6-force) --force overrides the edge-loop guard too.
	t.Run("edge_parent_child_under_closed_epic_force_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gef")
		epic := makeClosedParent(t, dir, "closed epic edge force", "epic")
		plan := `{"nodes":[{"key":"c","title":"forced edge child","type":"task"}],"edges":[{"from_key":"c","to_id":"` + epic + `","type":"parent-child"}]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "ef.json", plan), "--force")
		if res.IDs["c"] == "" {
			t.Errorf("--force should land the edge-linked child under a closed epic, got: %v", res.IDs)
		}
	})

	// (6-neg) an explicit parent-child EDGE under an OPEN epic still links — the
	//     edge-loop guard fires only on a CLOSED auto-closing parent (no over-fire).
	t.Run("edge_parent_child_under_open_epic_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "geo")
		epic := bdCreate(t, bd, dir, "open epic edge", "--type", "epic")
		plan := `{"nodes":[{"key":"c","title":"edge child open","type":"task"}],"edges":[{"from_key":"c","to_id":"` + epic.ID + `","type":"parent-child"}]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "eo.json", plan))
		if res.IDs["c"] == "" {
			t.Errorf("explicit parent-child edge under an OPEN epic should link, got: %v", res.IDs)
		}
	})
}
