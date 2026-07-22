package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestReadyWorkMolTypeEmitsClause pins the ready-work parity gap: BuildReadyWorkWhere
// must consume WorkFilter.MolType by emitting `mol_type = ?`, giving
// `bd ready --mol-type <t>` parity with `bd list --mol-type <t>` (filter.go:171).
// Before the fix the flag flowed into WorkFilter.MolType and reached only the wisp
// tier (ready_work.go readyWorkWispIssueFilter forwards it), while the MAIN
// ready-issues WHERE builder never emitted a mol_type predicate — so
// `bd ready --type molecule --mol-type swarm` silently returned ALL molecule
// subtypes, unlike bd list. The wisp-tier comment (beads-3y8y8) explicitly claims
// "identical semantics to the main ready-issues path (and bd list)" — this pins
// the main tier to that promise for MolType.
func TestReadyWorkMolTypeEmitsClause(t *testing.T) {
	mt := types.MolTypeSwarm
	where, args, err := BuildReadyWorkWhere(
		types.WorkFilter{MolType: &mt},
		IssuesFilterTables,
		ReadyWorkWhereInputs{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, "mol_type = ?") {
		t.Errorf("MolType produced no `mol_type = ?` predicate — filter silently ignored on the main ready tier: %q", where)
	}
	found := false
	for _, v := range args {
		if v == "swarm" {
			found = true
		}
	}
	if !found {
		t.Errorf("MolType arg 'swarm' not bound; args=%v", args)
	}
}

// TestReadyWorkMolTypeAbsentWhenUnset guards against a spurious mol_type clause
// when the flag is not set (the common path).
func TestReadyWorkMolTypeAbsentWhenUnset(t *testing.T) {
	where, _, err := BuildReadyWorkWhere(types.WorkFilter{}, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(where, "mol_type") {
		t.Errorf("unset MolType must not emit a mol_type predicate: %q", where)
	}
}
