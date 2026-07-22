package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestReadyWorkExcludeTypeComposesWithType pins beads-2a7n1: `bd ready` branched
// on filter.Type — when --type was set the ENTIRE else-branch (which merges the
// user's --exclude-type into ReadyWorkExcludeTypes) was skipped, so an explicit
// --exclude-type was silently dropped. bd list applies BOTH --type and
// --exclude-type (AND), so `--type X --exclude-type X` returns [] there but the
// bug made bd ready return the row (type-wins). The user's excludes must compose
// with --type; only the DEFAULT ready-work exclusions stay gated on the
// escape-hatch else-branch.
func TestReadyWorkExcludeTypeComposesWithType(t *testing.T) {
	t.Run("user --exclude-type emits a NOT IN clause even when --type is set", func(t *testing.T) {
		where, args, err := BuildReadyWorkWhere(
			types.WorkFilter{Type: "bug", ExcludeTypes: []types.IssueType{"bug"}},
			IssuesFilterTables, ReadyWorkWhereInputs{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(where, "issue_type = ?") {
			t.Errorf("--type should still emit issue_type = ?: %q", where)
		}
		// The whole point: the user's --exclude-type must still restrict. Without
		// the fix the else-branch is skipped and NO issue_type NOT IN is emitted.
		if !strings.Contains(where, "issue_type NOT IN") {
			t.Fatalf("beads-2a7n1: --exclude-type was silently dropped when --type is set — no issue_type NOT IN clause: %q", where)
		}
		// The excluded type must be bound as an arg (so the SQL actually filters it).
		var gotBug bool
		for _, v := range args {
			if v == "bug" {
				gotBug = true
			}
		}
		if !gotBug {
			t.Errorf("--exclude-type value not bound as arg: %v", args)
		}
	})

	t.Run("disjoint --type/--exclude-type both emit (list parity)", func(t *testing.T) {
		where, _, err := BuildReadyWorkWhere(
			types.WorkFilter{Type: "task", ExcludeTypes: []types.IssueType{"bug"}},
			IssuesFilterTables, ReadyWorkWhereInputs{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(where, "issue_type = ?") || !strings.Contains(where, "issue_type NOT IN") {
			t.Errorf("both --type and a disjoint --exclude-type should emit their clauses: %q", where)
		}
	})

	t.Run("--type WITHOUT --exclude-type does NOT emit a user NOT IN clause", func(t *testing.T) {
		// The escape-hatch: --type alone bypasses the default ready-work exclusions,
		// so there must be no issue_type NOT IN clause (the else-branch is skipped and
		// the user supplied no excludes).
		where, _, err := BuildReadyWorkWhere(
			types.WorkFilter{Type: "epic"},
			IssuesFilterTables, ReadyWorkWhereInputs{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(where, "issue_type NOT IN") {
			t.Errorf("--type alone must not emit a NOT IN clause (escape-hatch past default exclusions): %q", where)
		}
	})

	t.Run("empty --exclude-type entries are dropped (no spurious clause)", func(t *testing.T) {
		where, _, err := BuildReadyWorkWhere(
			types.WorkFilter{Type: "bug", ExcludeTypes: []types.IssueType{""}},
			IssuesFilterTables, ReadyWorkWhereInputs{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(where, "issue_type NOT IN") {
			t.Errorf("an empty-string --exclude-type entry must not emit a NOT IN clause: %q", where)
		}
	})

	t.Run("no --type: default ready-work exclusions still apply", func(t *testing.T) {
		// Guards the else-branch: without --type the default exclusion set
		// (merge-request/gate/molecule/epic/rig/infra) must still be emitted.
		where, _, err := BuildReadyWorkWhere(
			types.WorkFilter{}, IssuesFilterTables, ReadyWorkWhereInputs{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(where, "issue_type NOT IN") {
			t.Errorf("default ready-work exclusions must apply when --type is unset: %q", where)
		}
	})
}
