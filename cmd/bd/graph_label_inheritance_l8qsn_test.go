//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// beads-l8qsn (create-input-parity class, beads-7i4m/llzt graph-seam sibling):
// `bd create --parent P` inherits P's labels onto the child (union, unless
// --no-inherit-labels), but `bd create --graph` — where a node declares
// parent_id/parent_key establishing the SAME parent-child relationship —
// silently did NOT inherit them. Both graph paths (domain applyGraph +
// embedded graph_apply.go) agreed with each other but diverged from single
// create because Pass 1 creates every node top-level (no ParentID passed to
// create), so the create-time inheritance block never ran for graph children.
//
// FIX: a post-pass (after parent-child deps are linked, so a parent declared
// later in plan order via parent_key is already minted) reads parent labels and
// adds any the child lacks — mirroring single create's union semantics — in BOTH
// graph paths, honoring the plan-level --no-inherit-labels opt-out.
//
// End-to-end through the ACTUAL `bd create --graph` subprocess. bdShow hydrates
// Issue.Labels. MUTATION-VERIFIED: removing the Pass 4.5 inherit block (either
// path) leaves graph children with NO inherited labels (the pre-fix bug).
func TestEmbeddedGraphLabelInheritance_l8qsn(t *testing.T) {
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

	hasLabels := func(t *testing.T, bd, dir, id string, want ...string) {
		t.Helper()
		got := bdShow(t, bd, dir, id)
		set := make(map[string]bool, len(got.Labels))
		for _, l := range got.Labels {
			set[l] = true
		}
		for _, w := range want {
			if !set[w] {
				t.Errorf("issue %s: expected label %q, got %v", id, w, got.Labels)
			}
		}
	}

	lacksLabels := func(t *testing.T, bd, dir, id string, notWant ...string) {
		t.Helper()
		got := bdShow(t, bd, dir, id)
		set := make(map[string]bool, len(got.Labels))
		for _, l := range got.Labels {
			set[l] = true
		}
		for _, nw := range notWant {
			if set[nw] {
				t.Errorf("issue %s: did NOT expect label %q, got %v", id, nw, got.Labels)
			}
		}
	}

	// 1. graph node with parent_id to an already-existing labeled parent inherits
	//    the parent's labels, matching single create --parent.
	t.Run("parent_id_inherits_parent_labels", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gi")
		parent := bdCreate(t, bd, dir, "labeled parent", "-t", "epic", "-l", "area:core,team-a")

		plan := `{"nodes":[{"key":"c","title":"graph child","type":"task","parent_id":"` + parent.ID + `"}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "p1.json", plan))
		childID := res.IDs["c"]
		if childID == "" {
			t.Fatalf("no child id in result: %v", res.IDs)
		}
		hasLabels(t, bd, dir, childID, "area:core", "team-a")
	})

	// 2. graph node with parent_key (parent declared LATER in plan order) still
	//    inherits — proves the post-pass runs after IDs/linkage are resolved
	//    (order-independence).
	t.Run("parent_key_declared_later_inherits", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gk")
		// child node precedes the parent node in the plan; parent carries labels.
		plan := `{"nodes":[` +
			`{"key":"child","title":"graph child","type":"task","parent_key":"root"},` +
			`{"key":"root","title":"graph root","type":"epic","labels":["area:core","gate:x"]}` +
			`],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "p2.json", plan))
		childID := res.IDs["child"]
		if childID == "" {
			t.Fatalf("no child id in result: %v", res.IDs)
		}
		hasLabels(t, bd, dir, childID, "area:core", "gate:x")
	})

	// 3. child with its OWN explicit labels + a labeled parent → union, no dupes
	//    (matches single create's mergeCreateLabels union).
	t.Run("child_own_plus_inherited_union", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gu")
		parent := bdCreate(t, bd, dir, "labeled parent", "-t", "epic", "-l", "shared,parent-only")

		plan := `{"nodes":[{"key":"c","title":"graph child","type":"task","parent_id":"` + parent.ID + `","labels":["shared","child-only"]}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "p3.json", plan))
		childID := res.IDs["c"]
		if childID == "" {
			t.Fatalf("no child id in result: %v", res.IDs)
		}
		// child keeps its own labels AND gains parent's; "shared" not duplicated.
		got := bdShow(t, bd, dir, childID)
		count := map[string]int{}
		for _, l := range got.Labels {
			count[l]++
		}
		for _, want := range []string{"shared", "child-only", "parent-only"} {
			if count[want] == 0 {
				t.Errorf("expected label %q in union, got %v", want, got.Labels)
			}
		}
		if count["shared"] > 1 {
			t.Errorf("label 'shared' duplicated (%d) in union, got %v", count["shared"], got.Labels)
		}
	})

	// 4. plan-level opt-out: --no-inherit-labels suppresses inheritance for graph
	//    children (parity with `bd create --no-inherit-labels`), leaving only the
	//    child's own labels.
	t.Run("no_inherit_labels_opt_out", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gn")
		parent := bdCreate(t, bd, dir, "labeled parent", "-t", "epic", "-l", "parent-label")

		plan := `{"nodes":[{"key":"c","title":"graph child","type":"task","parent_id":"` + parent.ID + `","labels":["own-label"]}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "p4.json", plan), "--no-inherit-labels")
		childID := res.IDs["c"]
		if childID == "" {
			t.Fatalf("no child id in result: %v", res.IDs)
		}
		hasLabels(t, bd, dir, childID, "own-label")
		lacksLabels(t, bd, dir, childID, "parent-label")
	})

	// 5. negative: a graph node with NO parent is unaffected (keeps only its own
	//    labels; nothing spuriously inherited).
	t.Run("no_parent_node_unaffected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gp")
		plan := `{"nodes":[{"key":"solo","title":"parentless","type":"task","labels":["solo-label"]}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "p5.json", plan))
		soloID := res.IDs["solo"]
		if soloID == "" {
			t.Fatalf("no solo id in result: %v", res.IDs)
		}
		got := bdShow(t, bd, dir, soloID)
		if len(got.Labels) != 1 || got.Labels[0] != "solo-label" {
			t.Errorf("parentless node should keep only its own label, got %v", got.Labels)
		}
	})
}
