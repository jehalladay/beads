package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-6ymf: hermetic tests for the pure helpers in graph_apply.go (verified
// 0% + no test references). All are no-I/O — string/map/graph logic — so they
// need no fake or DB.

func TestGraphApplyDependencyType(t *testing.T) {
	// Empty defaults to blocks; anything else passes through verbatim.
	if got := graphApplyDependencyType(""); got != types.DepBlocks {
		t.Errorf("empty → %q, want blocks", got)
	}
	if got := graphApplyDependencyType("related"); got != types.DepRelated {
		t.Errorf("related → %q", got)
	}
	if got := graphApplyDependencyType("custom"); string(got) != "custom" {
		t.Errorf("custom passthrough → %q", got)
	}
}

func TestGraphApplyDependencyTypeClassifiers(t *testing.T) {
	// cycle-relevant = blocks | conditional-blocks
	if !graphApplyCycleRelevantDependencyType(types.DepBlocks) ||
		!graphApplyCycleRelevantDependencyType(types.DepConditionalBlocks) {
		t.Error("blocks/conditional-blocks should be cycle-relevant")
	}
	for _, d := range []types.DependencyType{types.DepRelated, types.DepParentChild, types.DepWaitsFor} {
		if graphApplyCycleRelevantDependencyType(d) {
			t.Errorf("%q should not be cycle-relevant", d)
		}
	}
	// ready-path mirrors AffectsReadyWork (blocks/parent-child/conditional/waits-for)
	if !graphApplyReadyPathDependencyType(types.DepParentChild) ||
		graphApplyReadyPathDependencyType(types.DepRelated) {
		t.Error("readyPathDependencyType should match AffectsReadyWork")
	}
}

func TestGraphApplySortedKeys(t *testing.T) {
	got := graphApplySortedKeys(map[string]bool{"c": true, "a": true, "b": true})
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("sortedKeys = %v, want [a b c]", got)
	}
	if len(graphApplySortedKeys(map[string]bool{})) != 0 {
		t.Error("empty map → empty slice")
	}
}

func TestGraphApplyDepPairKeyRoundTrip(t *testing.T) {
	key := graphApplyDepPairKey("issue-1", "dep-2")
	from, to, ok := graphApplyDepPairIDs(key)
	if !ok || from != "issue-1" || to != "dep-2" {
		t.Fatalf("round trip failed: from=%q to=%q ok=%v", from, to, ok)
	}
	// A string without the NUL separator is not a valid pair.
	if _, _, ok := graphApplyDepPairIDs("no-separator"); ok {
		t.Error("string without NUL separator should not parse as a pair")
	}
}

func TestResolveEdgeRef(t *testing.T) {
	keyToID := map[string]string{"k": "resolved-id"}
	if got := resolveEdgeRef("k", "explicit", keyToID); got != "explicit" {
		t.Errorf("explicit id wins: got %q", got)
	}
	if got := resolveEdgeRef("k", "", keyToID); got != "resolved-id" {
		t.Errorf("key resolves via map: got %q", got)
	}
	if got := resolveEdgeRef("", "", keyToID); got != "" {
		t.Errorf("no key/id → empty: got %q", got)
	}
	if got := resolveEdgeRef("missing", "", keyToID); got != "" {
		t.Errorf("unknown key → empty: got %q", got)
	}
}

func TestUnknownKeys(t *testing.T) {
	have := map[string]json.RawMessage{"title": nil, "bogus": nil, "weird": nil}
	known := map[string]struct{}{"title": {}}
	got := unknownKeys(have, known)
	if !reflect.DeepEqual(got, []string{"bogus", "weird"}) {
		t.Errorf("unknownKeys = %v, want sorted [bogus weird]", got)
	}
	if len(unknownKeys(map[string]json.RawMessage{"title": nil}, known)) != 0 {
		t.Error("all-known → no unknowns")
	}
}

func TestValidateGraphApplyLocalCycles(t *testing.T) {
	known := map[string]bool{"a": true, "b": true, "c": true}

	t.Run("acyclic plan passes", func(t *testing.T) {
		plan := &GraphApplyPlan{Edges: []GraphApplyEdge{
			{FromKey: "a", ToKey: "b", Type: "blocks"},
			{FromKey: "b", ToKey: "c", Type: "blocks"},
		}}
		if err := validateGraphApplyLocalCycles(plan, known); err != nil {
			t.Fatalf("acyclic plan should pass, got %v", err)
		}
	})

	t.Run("blocking cycle is rejected", func(t *testing.T) {
		plan := &GraphApplyPlan{Edges: []GraphApplyEdge{
			{FromKey: "a", ToKey: "b", Type: "blocks"},
			{FromKey: "b", ToKey: "a", Type: "blocks"},
		}}
		if err := validateGraphApplyLocalCycles(plan, known); err == nil {
			t.Fatal("blocking cycle should be rejected")
		}
	})

	t.Run("non-blocking cycle (related) is allowed", func(t *testing.T) {
		plan := &GraphApplyPlan{Edges: []GraphApplyEdge{
			{FromKey: "a", ToKey: "b", Type: "related"},
			{FromKey: "b", ToKey: "a", Type: "related"},
		}}
		if err := validateGraphApplyLocalCycles(plan, known); err != nil {
			t.Fatalf("related cycle should be allowed, got %v", err)
		}
	})

	t.Run("parent-key cycle is rejected", func(t *testing.T) {
		// Implicit parent-child edges (modeled by key) also form the graph.
		plan := &GraphApplyPlan{Nodes: []GraphApplyNode{
			{Key: "a", ParentKey: "b"},
			{Key: "b", ParentKey: "a"},
		}}
		if err := validateGraphApplyLocalCycles(plan, known); err == nil {
			t.Fatal("parent-key cycle should be rejected")
		}
	})

	t.Run("edge referencing an unknown key is ignored", func(t *testing.T) {
		plan := &GraphApplyPlan{Edges: []GraphApplyEdge{
			{FromKey: "a", ToKey: "external", Type: "blocks"},
		}}
		if err := validateGraphApplyLocalCycles(plan, known); err != nil {
			t.Fatalf("edge to unknown key should be skipped, got %v", err)
		}
	})
}
