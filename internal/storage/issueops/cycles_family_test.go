package issueops

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// beads-cjvxq: the create-time cycle invariant (cycleCheckTypesFor) and the
// read-side audit (DetectCyclesInTx) must protect the SAME edge-type families,
// or `bd dep cycles` reports a false "no cycles" all-clear on exactly the
// families the write guard exists to enforce. Both derive from
// cycleAuditedFamilies, so this asserts the invariant/audit pair can never
// drift: every type the write guard cycle-checks is a member of exactly one
// audited family, and vice versa.
func TestCycleAuditMatchesInvariant_cjvxq(t *testing.T) {
	t.Parallel()

	families := cycleAuditedFamilies()

	// Every member of every family must be cycle-checked by the write guard,
	// and cycleCheckTypesFor must return that same family (same source).
	membership := map[string]bool{}
	for _, family := range families {
		for _, member := range family {
			if membership[member] {
				t.Errorf("type %q appears in more than one audited family (families must be disjoint)", member)
			}
			membership[member] = true

			got := cycleCheckTypesFor(types.DependencyType(member))
			if len(got) != len(family) {
				t.Errorf("cycleCheckTypesFor(%q) = %v, want family %v", member, got, family)
				continue
			}
			for i := range family {
				if got[i] != family[i] {
					t.Errorf("cycleCheckTypesFor(%q) = %v, want family %v", member, got, family)
					break
				}
			}
		}
	}

	// The three families beads-8qij/8ix02 established must all be present, so a
	// future edit that drops one from the shared source fails loudly here.
	for _, must := range []types.DependencyType{
		types.DepBlocks, types.DepConditionalBlocks, types.DepParentChild, types.DepSupersedes,
	} {
		if !membership[string(must)] {
			t.Errorf("audited families missing must-be-acyclic type %q", must)
		}
	}

	// A non-cycle-checked association type must NOT be audited (guards against
	// accidentally graphing loose edges like relates-to/related).
	for _, notChecked := range []types.DependencyType{
		types.DepRelated, types.DepRelatesTo, types.DepDuplicates, types.DepWaitsFor, types.DepDiscoveredFrom,
	} {
		if membership[string(notChecked)] {
			t.Errorf("type %q must not be in an audited cycle family", notChecked)
		}
		if got := cycleCheckTypesFor(notChecked); got != nil {
			t.Errorf("cycleCheckTypesFor(%q) = %v, want nil (not cycle-checked)", notChecked, got)
		}
	}
}

// findCyclesInGraph is the pure DFS behind DetectCyclesInTx. Unit-test it
// independent of the DB (beads-cjvxq).
func TestFindCyclesInGraph_cjvxq(t *testing.T) {
	t.Parallel()

	t.Run("acyclic returns none", func(t *testing.T) {
		t.Parallel()
		g := map[string][]string{"a": {"b"}, "b": {"c"}}
		if got := findCyclesInGraph(g); len(got) != 0 {
			t.Fatalf("acyclic graph flagged: %v", got)
		}
	})

	t.Run("self edge is a cycle", func(t *testing.T) {
		t.Parallel()
		g := map[string][]string{"a": {"a"}}
		got := findCyclesInGraph(g)
		if len(got) != 1 || len(got[0]) != 1 || got[0][0] != "a" {
			t.Fatalf("self-edge: got %v, want [[a]]", got)
		}
	})

	t.Run("two-node cycle", func(t *testing.T) {
		t.Parallel()
		g := map[string][]string{"a": {"b"}, "b": {"a"}}
		got := findCyclesInGraph(g)
		if len(got) != 1 {
			t.Fatalf("two-node cycle: got %v, want one cycle", got)
		}
		if len(got[0]) != 2 {
			t.Fatalf("two-node cycle path len = %d, want 2 (%v)", len(got[0]), got[0])
		}
	})

	t.Run("three-node cycle", func(t *testing.T) {
		t.Parallel()
		g := map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}}
		got := findCyclesInGraph(g)
		if len(got) != 1 || len(got[0]) != 3 {
			t.Fatalf("three-node cycle: got %v, want one 3-node cycle", got)
		}
	})
}

// beads-cjvxq: appendGraphForTypesInTx must include ONLY the requested edge
// types. This is the seam that made parent-child / supersedes cycles invisible
// to the audit — the old blocks-only filter dropped those edges. Drive it with
// sqlmock so it is hermetic and fast (pure-Go).
func TestAppendGraphForTypesInTx_filtersToRequestedTypes_cjvxq(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)

	rows := sqlmock.NewRows([]string{"issue_id", "depends_on_id", "type"}).
		AddRow("a", "b", string(types.DepParentChild)).
		AddRow("b", "a", string(types.DepParentChild)).
		AddRow("a", "z", string(types.DepBlocks)).       // wrong family — must be dropped
		AddRow("a", "r", string(types.DepRelated))       // loose edge — must be dropped
	mock.ExpectQuery(regexp.QuoteMeta("FROM dependencies")).WillReturnRows(rows)

	graph := make(map[string][]string)
	if err := appendGraphForTypesInTx(context.Background(), tx, []string{"dependencies"},
		[]string{string(types.DepParentChild)}, graph); err != nil {
		t.Fatalf("appendGraphForTypesInTx: %v", err)
	}

	// Only the parent-child edges survive; the blocks + related edges are gone.
	if got := graph["a"]; len(got) != 1 || got[0] != "b" {
		t.Errorf("graph[a] = %v, want [b] (only parent-child edge)", got)
	}
	if got := graph["b"]; len(got) != 1 || got[0] != "a" {
		t.Errorf("graph[b] = %v, want [a]", got)
	}

	// The resulting parent-child graph has a cycle a<->b.
	cycles := findCyclesInGraph(graph)
	if len(cycles) != 1 {
		t.Fatalf("expected one parent-child cycle, got %v", cycles)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
