package issueops

import (
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestReachPath(t *testing.T) {
	t.Parallel()

	// a -> b -> c -> d ; a -> e (dead end)
	graph := map[string][]string{
		"a": {"b", "e"},
		"b": {"c"},
		"c": {"d"},
	}

	t.Run("start equals goal returns single node", func(t *testing.T) {
		t.Parallel()
		got := reachPath(graph, "a", "a")
		if len(got) != 1 || got[0] != "a" {
			t.Fatalf("reachPath(a,a) = %v, want [a]", got)
		}
	})

	t.Run("multi-hop path is reconstructed start-to-goal", func(t *testing.T) {
		t.Parallel()
		got := reachPath(graph, "a", "d")
		want := []string{"a", "b", "c", "d"}
		if len(got) != len(want) {
			t.Fatalf("reachPath(a,d) = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("reachPath(a,d)[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("unreachable goal returns nil", func(t *testing.T) {
		t.Parallel()
		if got := reachPath(graph, "e", "d"); got != nil {
			t.Fatalf("reachPath(e,d) = %v, want nil", got)
		}
	})

	t.Run("missing start node returns nil", func(t *testing.T) {
		t.Parallel()
		if got := reachPath(graph, "zz", "d"); got != nil {
			t.Fatalf("reachPath(zz,d) = %v, want nil", got)
		}
	})
}

func TestCycleThroughEdgesInGraphWithReachPath(t *testing.T) {
	t.Parallel()

	t.Run("self-edge reports a trivial cycle", func(t *testing.T) {
		t.Parallel()
		got := CycleThroughEdgesInGraph(map[string][]string{}, [][2]string{{"x", "x"}})
		if got != "x → x" {
			t.Fatalf("self-edge cycle = %q, want \"x → x\"", got)
		}
	})

	t.Run("new edge on a cycle is rendered", func(t *testing.T) {
		t.Parallel()
		// Edge a->b closes a cycle because b ⇝ a already exists (b->c->a).
		graph := map[string][]string{
			"a": {"b"},
			"b": {"c"},
			"c": {"a"},
		}
		got := CycleThroughEdgesInGraph(graph, [][2]string{{"a", "b"}})
		if got != "a → b → c → a" {
			t.Fatalf("cycle = %q, want \"a → b → c → a\"", got)
		}
	})

	t.Run("edge not on any cycle returns empty", func(t *testing.T) {
		t.Parallel()
		graph := map[string][]string{"a": {"b"}}
		if got := CycleThroughEdgesInGraph(graph, [][2]string{{"a", "b"}}); got != "" {
			t.Fatalf("non-cycle edge = %q, want empty", got)
		}
	})

	t.Run("empty endpoints are skipped", func(t *testing.T) {
		t.Parallel()
		if got := CycleThroughEdgesInGraph(map[string][]string{}, [][2]string{{"", "b"}, {"a", ""}}); got != "" {
			t.Fatalf("empty-endpoint edges = %q, want empty", got)
		}
	})
}

func TestDedupePreservingOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil in -> empty", in: nil, want: []string{}},
		{name: "dedup keeps first occurrence order", in: []string{"b", "a", "b", "c", "a"}, want: []string{"b", "a", "c"}},
		{name: "trims and skips empty/whitespace", in: []string{" a ", "", "  ", "a"}, want: []string{"a"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := dedupePreservingOrder(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("dedupePreservingOrder(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("dedupePreservingOrder(%v)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMergeChangedTables(t *testing.T) {
	t.Parallel()

	t.Run("nil dst is initialized", func(t *testing.T) {
		t.Parallel()
		got := mergeChangedTables(nil, map[string]bool{"issues": true})
		if got == nil || !got["issues"] {
			t.Fatalf("mergeChangedTables(nil, {issues}) = %v, want {issues:true}", got)
		}
	})

	t.Run("union into existing dst", func(t *testing.T) {
		t.Parallel()
		dst := map[string]bool{"a": true}
		got := mergeChangedTables(dst, map[string]bool{"b": true, "c": true})
		for _, k := range []string{"a", "b", "c"} {
			if !got[k] {
				t.Errorf("merged missing %q: %v", k, got)
			}
		}
	})

	t.Run("empty src leaves dst untouched", func(t *testing.T) {
		t.Parallel()
		dst := map[string]bool{"a": true}
		got := mergeChangedTables(dst, map[string]bool{})
		if len(got) != 1 || !got["a"] {
			t.Fatalf("merged with empty src = %v, want {a:true}", got)
		}
	})
}

func TestStageableChangedTables(t *testing.T) {
	t.Parallel()

	in := map[string]bool{
		"issues":       true,
		"dependencies": true,
		"wisps":        true,
		"wisp_events":  true,
		"wisp_deps":    true,
	}
	got := stageableChangedTables(in)
	if !got["issues"] || !got["dependencies"] {
		t.Errorf("expected issues+dependencies retained, got %v", got)
	}
	for _, dropped := range []string{"wisps", "wisp_events", "wisp_deps"} {
		if got[dropped] {
			t.Errorf("expected %q dropped, got %v", dropped, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("expected 2 stageable tables, got %d: %v", len(got), got)
	}
}

func TestRemoveSourceFromAffected(t *testing.T) {
	t.Parallel()

	t.Run("regular source removed from issue bucket only", func(t *testing.T) {
		t.Parallel()
		issueIDs := []string{"bd-1", "bd-2"}
		wispIDs := []string{"bd-1"}
		gotIssues, gotWisps := RemoveSourceFromAffected("bd-1", false, issueIDs, wispIDs)
		if len(gotIssues) != 1 || gotIssues[0] != "bd-2" {
			t.Fatalf("issue bucket = %v, want [bd-2]", gotIssues)
		}
		if len(gotWisps) != 1 || gotWisps[0] != "bd-1" {
			t.Fatalf("wisp bucket = %v, want [bd-1] (untouched)", gotWisps)
		}
	})

	t.Run("wisp source removed from wisp bucket only", func(t *testing.T) {
		t.Parallel()
		gotIssues, gotWisps := RemoveSourceFromAffected("w-1", true, []string{"w-1"}, []string{"w-1", "w-2"})
		if len(gotIssues) != 1 || gotIssues[0] != "w-1" {
			t.Fatalf("issue bucket = %v, want [w-1] (untouched)", gotIssues)
		}
		if len(gotWisps) != 1 || gotWisps[0] != "w-2" {
			t.Fatalf("wisp bucket = %v, want [w-2]", gotWisps)
		}
	})
}

func TestRemoveID(t *testing.T) {
	t.Parallel()

	t.Run("empty slice passthrough", func(t *testing.T) {
		t.Parallel()
		if got := removeID(nil, "x"); len(got) != 0 {
			t.Fatalf("removeID(nil,x) = %v, want empty", got)
		}
	})

	t.Run("removes all matching entries", func(t *testing.T) {
		t.Parallel()
		got := removeID([]string{"a", "x", "b", "x"}, "x")
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("removeID = %v, want [a b]", got)
		}
	})

	t.Run("no match keeps all", func(t *testing.T) {
		t.Parallel()
		got := removeID([]string{"a", "b"}, "z")
		if len(got) != 2 {
			t.Fatalf("removeID = %v, want [a b]", got)
		}
	})
}

func TestIsNothingToCommitError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "nothing to commit", err: errors.New("nothing to commit, working tree clean"), want: true},
		{name: "case-insensitive", err: errors.New("NOTHING TO COMMIT"), want: true},
		{name: "no changes ... commit", err: errors.New("no changes added to commit"), want: true},
		{name: "no changes without commit word", err: errors.New("no changes detected"), want: false},
		{name: "unrelated error", err: errors.New("disk full"), want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsNothingToCommitError(tt.err); got != tt.want {
				t.Fatalf("IsNothingToCommitError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDependencyCreatedBy(t *testing.T) {
	t.Parallel()

	t.Run("uses dep.CreatedBy when set", func(t *testing.T) {
		t.Parallel()
		if got := dependencyCreatedBy(&types.Dependency{CreatedBy: "alice"}, "actor"); got != "alice" {
			t.Fatalf("dependencyCreatedBy = %q, want alice", got)
		}
	})

	t.Run("falls back to actor when CreatedBy empty", func(t *testing.T) {
		t.Parallel()
		if got := dependencyCreatedBy(&types.Dependency{}, "actor"); got != "actor" {
			t.Fatalf("dependencyCreatedBy = %q, want actor", got)
		}
	})

	t.Run("falls back to actor when dep is nil", func(t *testing.T) {
		t.Parallel()
		if got := dependencyCreatedBy(nil, "actor"); got != "actor" {
			t.Fatalf("dependencyCreatedBy(nil) = %q, want actor", got)
		}
	})
}
