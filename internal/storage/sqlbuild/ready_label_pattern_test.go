package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestReadyWorkLabelPatternEmitsLike pins beads-v8e8 for the ready-work path:
// BuildReadyWorkWhere must consume WorkFilter.LabelPattern by emitting a
// case-insensitive LIKE subquery with the glob translated to a SQL LIKE arg
// (via globToLike), giving `bd ready --label-pattern` parity with `bd list`
// (beads-v5i7). Without the clause the flag flowed into the filter but no
// ready query consumed it → silently ignored, returning everything.
func TestReadyWorkLabelPatternEmitsLike(t *testing.T) {
	where, args, err := BuildReadyWorkWhere(
		types.WorkFilter{LabelPattern: "tech-*"},
		IssuesFilterTables,
		ReadyWorkWhereInputs{},
	)
	if err != nil {
		t.Fatal(err)
	}
	up := strings.ToUpper(where)
	if !strings.Contains(up, "LIKE") {
		t.Errorf("LabelPattern produced no LIKE predicate — filter silently ignored: %q", where)
	}
	if !strings.Contains(where, "LOWER(label) LIKE LOWER(?)") {
		t.Errorf("LabelPattern LIKE should be case-insensitive (LOWER both sides): %q", where)
	}
	// The glob 'tech-*' must be translated to a SQL LIKE arg 'tech-%'.
	found := false
	for _, v := range args {
		if v == "tech-%" {
			found = true
		}
	}
	if !found {
		t.Errorf("glob 'tech-*' should translate to LIKE arg 'tech-%%'; args=%v", args)
	}
}

// TestReadyWorkLabelRegexEmitsRegexp pins beads-v8e8: --label-regex must emit a
// SQL REGEXP subquery in the ready path (mirroring bd list), binding the raw
// regex as its arg.
func TestReadyWorkLabelRegexEmitsRegexp(t *testing.T) {
	where, args, err := BuildReadyWorkWhere(
		types.WorkFilter{LabelRegex: "tech-(debt|legacy)"},
		IssuesFilterTables,
		ReadyWorkWhereInputs{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(where), "REGEXP") {
		t.Errorf("LabelRegex produced no REGEXP predicate — filter silently ignored: %q", where)
	}
	found := false
	for _, v := range args {
		if v == "tech-(debt|legacy)" {
			found = true
		}
	}
	if !found {
		t.Errorf("regex arg not bound; args=%v", args)
	}
}

// TestReadyWorkLabelPatternAbsentWhenUnset guards against a spurious clause when
// neither flag is set (the common path) — no LIKE/REGEXP from these fields.
func TestReadyWorkLabelPatternAbsentWhenUnset(t *testing.T) {
	where, _, err := BuildReadyWorkWhere(types.WorkFilter{}, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatal(err)
	}
	// The identity-label exclusion uses NOT IN, never LIKE/REGEXP, so neither
	// token should appear absent the pattern/regex filters.
	if strings.Contains(strings.ToUpper(where), "LIKE") || strings.Contains(strings.ToUpper(where), "REGEXP") {
		t.Errorf("unset LabelPattern/LabelRegex must not emit LIKE/REGEXP: %q", where)
	}
}
