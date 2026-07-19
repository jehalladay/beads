package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-inb4: `bd ready --include-deferred` was a dead flag. The CLI ready path
// passes Status:"open"; BuildReadyWorkWhere turned that into `status = 'open'`,
// which excludes deferred-STATUS rows before the defer_until relaxation could
// matter. Since the only way to set a future defer_until (create/update --defer
// <future>) ALSO flips status→deferred, the flag changed the result by 0 rows.
// Fix: when IncludeDeferred is set, the status clause must also admit
// 'deferred' so upcoming deferred work actually surfaces.

func TestReadyIncludeDeferredWidensStatusClause(t *testing.T) {
	t.Parallel()

	// The CLI ready path: an explicit single status plus --include-deferred.
	where, args, err := BuildReadyWorkWhere(
		types.WorkFilter{Status: types.StatusOpen, IncludeDeferred: true},
		IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The status clause must widen to a multi-status match (the values are
	// parameterized, so assert the clause shape + the arg below).
	if !strings.Contains(where, "status IN (?") {
		t.Errorf("with --include-deferred the status clause must widen to a multi-status IN, where = %q", where)
	}
	// 'deferred' must appear as a status arg (not only as a substring of a column).
	foundDeferredArg := false
	for _, a := range args {
		if s, ok := a.(string); ok && s == string(types.StatusDeferred) {
			foundDeferredArg = true
		}
	}
	if !foundDeferredArg {
		t.Errorf("expected 'deferred' among status args, got %v", args)
	}
	// The defer_until relaxation still applies (no defer_until clause when included).
	if strings.Contains(where, "defer_until <= UTC_TIMESTAMP()") {
		t.Errorf("with IncludeDeferred the defer_until predicate must be relaxed, where = %q", where)
	}
}

func TestReadyDefaultStatusUnchangedWithoutIncludeDeferred(t *testing.T) {
	t.Parallel()

	// Without --include-deferred, an explicit single status stays a single-status
	// match and the defer_until predicate is enforced (no regression).
	where, args, err := BuildReadyWorkWhere(
		types.WorkFilter{Status: types.StatusOpen}, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, a := range args {
		if s, ok := a.(string); ok && s == string(types.StatusDeferred) {
			t.Errorf("without IncludeDeferred, 'deferred' must not be admitted, args = %v", args)
		}
	}
	if !strings.Contains(where, "defer_until") {
		t.Errorf("without IncludeDeferred the defer_until predicate must be present, where = %q", where)
	}
}
