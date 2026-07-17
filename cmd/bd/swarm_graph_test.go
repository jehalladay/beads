package main

import (
	"strings"
	"testing"
)

// beads-99vk: hermetic tests for the pure graph-analysis helpers in swarm.go
// (verified 0% + no test references). computeReadyFronts and
// detectStructuralIssues operate entirely on an in-memory *SwarmAnalysis — no
// storage, no I/O.

// node is a small builder for an IssueNode in a SwarmAnalysis graph.
func swarmAnalysis(nodes ...*IssueNode) *SwarmAnalysis {
	a := &SwarmAnalysis{Issues: map[string]*IssueNode{}}
	for _, n := range nodes {
		a.Issues[n.ID] = n
		a.TotalIssues++
	}
	return a
}

func TestComputeReadyFronts(t *testing.T) {
	t.Run("linear chain → one issue per wave", func(t *testing.T) {
		// a → b → c  (a depended-on-by b, etc.)
		a := swarmAnalysis(
			&IssueNode{ID: "a", DependedOnBy: []string{"b"}},
			&IssueNode{ID: "b", DependsOn: []string{"a"}, DependedOnBy: []string{"c"}},
			&IssueNode{ID: "c", DependsOn: []string{"b"}},
		)
		computeReadyFronts(a)
		if len(a.ReadyFronts) != 3 {
			t.Fatalf("expected 3 waves, got %d: %+v", len(a.ReadyFronts), a.ReadyFronts)
		}
		if a.ReadyFronts[0].Issues[0] != "a" || a.ReadyFronts[2].Issues[0] != "c" {
			t.Errorf("wrong wave ordering: %+v", a.ReadyFronts)
		}
		if a.MaxParallelism != 1 {
			t.Errorf("MaxParallelism = %d, want 1", a.MaxParallelism)
		}
		if a.Issues["c"].Wave != 2 {
			t.Errorf("c.Wave = %d, want 2", a.Issues["c"].Wave)
		}
		if a.EstimatedSessions != 3 {
			t.Errorf("EstimatedSessions = %d, want 3", a.EstimatedSessions)
		}
	})

	t.Run("diamond → parallel middle wave", func(t *testing.T) {
		// a → {b, c} → d
		a := swarmAnalysis(
			&IssueNode{ID: "a", DependedOnBy: []string{"b", "c"}},
			&IssueNode{ID: "b", DependsOn: []string{"a"}, DependedOnBy: []string{"d"}},
			&IssueNode{ID: "c", DependsOn: []string{"a"}, DependedOnBy: []string{"d"}},
			&IssueNode{ID: "d", DependsOn: []string{"b", "c"}},
		)
		computeReadyFronts(a)
		if len(a.ReadyFronts) != 3 {
			t.Fatalf("expected 3 waves, got %d", len(a.ReadyFronts))
		}
		// Wave 1 runs b and c in parallel, sorted deterministically.
		w1 := a.ReadyFronts[1].Issues
		if len(w1) != 2 || w1[0] != "b" || w1[1] != "c" {
			t.Errorf("wave 1 = %v, want [b c]", w1)
		}
		if a.MaxParallelism != 2 {
			t.Errorf("MaxParallelism = %d, want 2", a.MaxParallelism)
		}
	})

	t.Run("errors present → no computation", func(t *testing.T) {
		a := swarmAnalysis(&IssueNode{ID: "a"})
		a.Errors = []string{"cycle detected"}
		computeReadyFronts(a)
		if len(a.ReadyFronts) != 0 {
			t.Errorf("with errors, expected no ready fronts, got %+v", a.ReadyFronts)
		}
	})
}

func TestDetectStructuralIssues(t *testing.T) {
	t.Run("clean DAG → no errors, no cycle", func(t *testing.T) {
		a := swarmAnalysis(
			&IssueNode{ID: "a", Title: "impl a", DependedOnBy: []string{"b"}},
			&IssueNode{ID: "b", Title: "impl b", DependsOn: []string{"a"}},
		)
		detectStructuralIssues(a, nil)
		if len(a.Errors) != 0 {
			t.Errorf("clean DAG should have no errors, got %v", a.Errors)
		}
	})

	t.Run("cycle is detected as an error", func(t *testing.T) {
		// a ↔ b
		a := swarmAnalysis(
			&IssueNode{ID: "a", Title: "a", DependsOn: []string{"b"}, DependedOnBy: []string{"b"}},
			&IssueNode{ID: "b", Title: "b", DependsOn: []string{"a"}, DependedOnBy: []string{"a"}},
		)
		detectStructuralIssues(a, nil)
		joined := strings.Join(a.Errors, " ")
		if !strings.Contains(joined, "cycle") {
			t.Errorf("expected a cycle error, got %v", a.Errors)
		}
	})

	t.Run("foundation with no dependents warns", func(t *testing.T) {
		a := swarmAnalysis(
			&IssueNode{ID: "a", Title: "Foundation setup"}, // no DependedOnBy
		)
		detectStructuralIssues(a, nil)
		if !warnsContain(a.Warnings, "no dependents") {
			t.Errorf("expected a foundation-no-dependents warning, got %v", a.Warnings)
		}
	})

	t.Run("integration with no dependencies warns", func(t *testing.T) {
		a := swarmAnalysis(
			&IssueNode{ID: "a", Title: "Integration final", DependedOnBy: []string{"b"}},
			&IssueNode{ID: "b", Title: "impl", DependsOn: []string{"a"}},
		)
		detectStructuralIssues(a, nil)
		if !warnsContain(a.Warnings, "no dependencies") {
			t.Errorf("expected an integration-no-dependencies warning, got %v", a.Warnings)
		}
	})

	t.Run("disconnected subgraph warns", func(t *testing.T) {
		// a→b connected; c→d island unreachable from a's root, but c IS a root
		// so it's reachable — to force disconnection, make an island where the
		// only root is a and the island {x→y} has its root x not linked.
		a := swarmAnalysis(
			&IssueNode{ID: "a", Title: "root a", DependedOnBy: []string{"b"}},
			&IssueNode{ID: "b", Title: "b", DependsOn: []string{"a"}},
			// island: x is a root (no deps) reachable; make y depend on b-less node
			&IssueNode{ID: "y", Title: "orphan y", DependsOn: []string{"ghost"}},
		)
		detectStructuralIssues(a, nil)
		// y depends on a node not in the graph and has no in-graph root path →
		// it is not reached from any real root, so it is flagged disconnected.
		if !warnsContain(a.Warnings, "Disconnected") {
			t.Errorf("expected a disconnected-issues warning, got %v", a.Warnings)
		}
	})
}

func warnsContain(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
