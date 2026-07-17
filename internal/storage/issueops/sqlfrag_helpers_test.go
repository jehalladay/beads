package issueops

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestWispFilterToIssueFilter(t *testing.T) {
	t.Parallel()

	t.Run("always sets Ephemeral=true", func(t *testing.T) {
		t.Parallel()
		got := WispFilterToIssueFilter(types.WispFilter{})
		if got.Ephemeral == nil || !*got.Ephemeral {
			t.Fatalf("Ephemeral = %v, want true", got.Ephemeral)
		}
	})

	t.Run("maps type/status/time/limit fields", func(t *testing.T) {
		t.Parallel()
		typ := types.TypeTask
		st := types.StatusInProgress
		after := time.Now().Add(-time.Hour)
		before := time.Now()
		got := WispFilterToIssueFilter(types.WispFilter{
			Type:          &typ,
			Status:        &st,
			UpdatedAfter:  &after,
			UpdatedBefore: &before,
			Limit:         7,
		})
		if got.IssueType != &typ || got.Status != &st {
			t.Errorf("type/status not passed through")
		}
		if got.UpdatedAfter != &after || got.UpdatedBefore != &before {
			t.Errorf("time bounds not passed through")
		}
		if got.Limit != 7 {
			t.Errorf("Limit = %d, want 7", got.Limit)
		}
	})

	t.Run("excludes closed when no status and IncludeClosed=false", func(t *testing.T) {
		t.Parallel()
		got := WispFilterToIssueFilter(types.WispFilter{})
		if len(got.ExcludeStatus) != 1 || got.ExcludeStatus[0] != types.StatusClosed {
			t.Fatalf("ExcludeStatus = %v, want [closed]", got.ExcludeStatus)
		}
	})

	t.Run("does not exclude closed when IncludeClosed=true", func(t *testing.T) {
		t.Parallel()
		got := WispFilterToIssueFilter(types.WispFilter{IncludeClosed: true})
		if len(got.ExcludeStatus) != 0 {
			t.Fatalf("ExcludeStatus = %v, want empty when IncludeClosed", got.ExcludeStatus)
		}
	})

	t.Run("does not add ExcludeStatus when an explicit status is set", func(t *testing.T) {
		t.Parallel()
		st := types.StatusOpen
		got := WispFilterToIssueFilter(types.WispFilter{Status: &st})
		if len(got.ExcludeStatus) != 0 {
			t.Fatalf("ExcludeStatus = %v, want empty when Status set", got.ExcludeStatus)
		}
	})
}

func TestJoinAnd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		clauses []string
		want    string
	}{
		{name: "empty", clauses: nil, want: ""},
		{name: "single clause returned as-is", clauses: []string{"a = 1"}, want: "a = 1"},
		{name: "two clauses joined with AND", clauses: []string{"a = 1", "b = 2"}, want: "a = 1 AND b = 2"},
		{name: "three clauses", clauses: []string{"a", "b", "c"}, want: "a AND b AND c"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := joinAnd(tt.clauses); got != tt.want {
				t.Fatalf("joinAnd(%v) = %q, want %q", tt.clauses, got, tt.want)
			}
		})
	}
}

func TestDepTargetExpr(t *testing.T) {
	t.Parallel()

	t.Run("empty alias returns the shared constant", func(t *testing.T) {
		t.Parallel()
		if got := depTargetExpr(""); got != DepTargetExpr {
			t.Fatalf("depTargetExpr(\"\") = %q, want DepTargetExpr %q", got, DepTargetExpr)
		}
	})

	t.Run("aliased form qualifies each column with the alias", func(t *testing.T) {
		t.Parallel()
		got := depTargetExpr("d")
		for _, want := range []string{"COALESCE(", "d.depends_on_issue_id", "d.depends_on_wisp_id", "d.depends_on_external"} {
			if !strings.Contains(got, want) {
				t.Errorf("depTargetExpr(d) = %q, missing %q", got, want)
			}
		}
	})
}

func TestDepTargetEqualsAndIn(t *testing.T) {
	t.Parallel()

	eq := depTargetEquals("d")
	if !strings.HasSuffix(eq, " = ?") || !strings.HasPrefix(eq, depTargetExpr("d")) {
		t.Errorf("depTargetEquals(d) = %q, want depTargetExpr(d) + \" = ?\"", eq)
	}

	in := depTargetIn("d", "?,?,?")
	if !strings.HasPrefix(in, depTargetExpr("d")) || !strings.HasSuffix(in, " IN (?,?,?)") {
		t.Errorf("depTargetIn(d,?,?,?) = %q, want expr + \" IN (?,?,?)\"", in)
	}
}

func TestReadyWorkExcludeTypes(t *testing.T) {
	t.Parallel()

	t.Run("includes the base excluded types", func(t *testing.T) {
		t.Parallel()
		got := readyWorkExcludeTypes(nil)
		set := make(map[types.IssueType]bool, len(got))
		for _, tp := range got {
			set[tp] = true
		}
		for _, want := range []types.IssueType{"merge-request", types.TypeGate, types.TypeMolecule, "rig"} {
			if !set[want] {
				t.Errorf("readyWorkExcludeTypes missing base type %q: %v", want, got)
			}
		}
	})

	t.Run("extra types are appended and deduped", func(t *testing.T) {
		t.Parallel()
		base := readyWorkExcludeTypes(nil)
		withExtra := readyWorkExcludeTypes([]types.IssueType{"custom-x", types.TypeGate, "", "custom-x"})
		// "custom-x" added once; the duplicate TypeGate and empty are dropped.
		if len(withExtra) != len(base)+1 {
			t.Fatalf("expected exactly one net-new type, base=%d withExtra=%d", len(base), len(withExtra))
		}
		found := false
		for _, tp := range withExtra {
			if tp == "custom-x" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected custom-x in result: %v", withExtra)
		}
	})
}
