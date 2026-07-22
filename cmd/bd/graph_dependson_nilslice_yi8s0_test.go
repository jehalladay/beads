//go:build cgo

package main

import (
	"encoding/json"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestComputeLayout_DependsOnNeverNil_yi8s0 pins the JSON contract for the
// per-node depends_on field emitted by `bd graph <id> --json` (beads-yi8s0).
//
// GraphNode.DependsOn ([]string, json:"depends_on", NO omitempty) is populated
// from a map[string][]string built only from `blocks` dependencies. Any node
// with no incoming blocks-dep (every leaf, and roots that block nothing) misses
// the map → a nil slice → marshals to "depends_on":null instead of []. This is
// the wgvo1 nil-slice→JSON-array sibling-divergence: the same file already
// guards layout.Layers (make), `graph check` cycles (beads-8wyu), and
// `graph --all` dependencies — the per-node field was the missed sibling.
//
// Teeth: assert DependsOn is non-nil AND that it marshals to [] (not null) for
// a leaf node. Reverting the computeLayout coalesce (DependsOn: dependsOn[id])
// makes the leaf's slice nil again → both assertions fail.
func TestComputeLayout_DependsOnNeverNil_yi8s0(t *testing.T) {
	// issue-2 depends on issue-1 (blocks edge). issue-1 is a leaf: nothing
	// blocks it, so its dependsOn map entry is absent = the nil-slice case.
	subgraph := &TemplateSubgraph{
		Root: &types.Issue{ID: "issue-1"},
		Issues: []*types.Issue{
			{ID: "issue-1", Title: "Leaf blocker", Status: types.StatusOpen},
			{ID: "issue-2", Title: "Dependent", Status: types.StatusOpen},
		},
		Dependencies: []*types.Dependency{
			{IssueID: "issue-2", DependsOnID: "issue-1", Type: types.DepBlocks},
		},
	}

	layout := computeLayout(subgraph)

	leaf := layout.Nodes["issue-1"]
	if leaf == nil {
		t.Fatalf("expected a node for issue-1")
	}
	if leaf.DependsOn == nil {
		t.Fatalf("leaf node depends_on is nil — must be an empty slice so JSON emits [] not null (beads-yi8s0)")
	}
	if len(leaf.DependsOn) != 0 {
		t.Fatalf("leaf node depends_on = %v, want empty", leaf.DependsOn)
	}

	// The node WITH a blocks-dep must still carry its real edge.
	dependent := layout.Nodes["issue-2"]
	if dependent == nil {
		t.Fatalf("expected a node for issue-2")
	}
	if len(dependent.DependsOn) != 1 || dependent.DependsOn[0] != "issue-1" {
		t.Fatalf("dependent node depends_on = %v, want [issue-1]", dependent.DependsOn)
	}

	// Contract-level: the leaf must serialize to [] not null.
	b, err := json.Marshal(leaf)
	if err != nil {
		t.Fatalf("marshal leaf node: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal leaf node: %v", err)
	}
	if got := string(m["depends_on"]); got != "[]" {
		t.Fatalf("leaf depends_on JSON = %s, want [] (beads-yi8s0 nil-slice→null)", got)
	}
}
