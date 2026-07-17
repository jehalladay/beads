package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestReadyWorkAssigneeCaseInsensitive pins beads-xl4k for the ready-work path:
// BuildReadyWorkWhere must match assignee case-insensitively (LOWER both sides),
// consistent with the bd list/query filter + predicate paths, so
// `bd ready --assignee Alice` finds an issue assigned "alice".
func TestReadyWorkAssigneeCaseInsensitive(t *testing.T) {
	a := "Alice"
	where, args, err := BuildReadyWorkWhere(types.WorkFilter{Assignee: &a}, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, "LOWER(assignee) = LOWER(?)") {
		t.Errorf("ready-work assignee match not case-insensitive: %q", where)
	}
	found := false
	for _, v := range args {
		if v == "Alice" {
			found = true
		}
	}
	if !found {
		t.Errorf("assignee arg not bound; args=%v", args)
	}
}
