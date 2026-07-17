package issueops

import (
	"database/sql"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestReadyWorkPageSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "zero limit means unbounded (0)", limit: 0, want: 0},
		{name: "negative limit means unbounded (0)", limit: -5, want: 0},
		{name: "small limit floored to 100", limit: 10, want: 100},
		{name: "exactly at floor", limit: 100, want: 100},
		{name: "large limit passes through", limit: 500, want: 500},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := readyWorkPageSize(tt.limit); got != tt.want {
				t.Fatalf("readyWorkPageSize(%d) = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}

func TestWispTableRouting(t *testing.T) {
	t.Parallel()

	t.Run("wisp routes to wisp_* tables", func(t *testing.T) {
		t.Parallel()
		i, l, e, d := WispTableRouting(true)
		if i != "wisps" || l != "wisp_labels" || e != "wisp_events" || d != "wisp_dependencies" {
			t.Fatalf("wisp routing = (%q,%q,%q,%q), want wisp_* tables", i, l, e, d)
		}
	})

	t.Run("non-wisp routes to regular tables", func(t *testing.T) {
		t.Parallel()
		i, l, e, d := WispTableRouting(false)
		if i != "issues" || l != "labels" || e != "events" || d != "dependencies" {
			t.Fatalf("regular routing = (%q,%q,%q,%q), want regular tables", i, l, e, d)
		}
	})
}

func TestNullStringValue(t *testing.T) {
	t.Parallel()

	if got := nullStringValue(sql.NullString{Valid: false}); got != nil {
		t.Errorf("invalid NullString = %v, want nil", got)
	}
	got := nullStringValue(sql.NullString{Valid: true, String: "hi"})
	if s, ok := got.(string); !ok || s != "hi" {
		t.Errorf("valid NullString = %v, want \"hi\"", got)
	}
}

func TestNullTimeValue(t *testing.T) {
	t.Parallel()

	if got := nullTimeValue(sql.NullTime{Valid: false}); got != nil {
		t.Errorf("invalid NullTime = %v, want nil", got)
	}
	now := time.Now().UTC()
	got := nullTimeValue(sql.NullTime{Valid: true, Time: now})
	if tv, ok := got.(time.Time); !ok || !tv.Equal(now) {
		t.Errorf("valid NullTime = %v, want %v", got, now)
	}
}

func TestIssuePriorityBefore(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	later := base.Add(time.Hour)

	t.Run("lower priority sorts first", func(t *testing.T) {
		t.Parallel()
		a := &types.Issue{ID: "a", Priority: 0, CreatedAt: base}
		b := &types.Issue{ID: "b", Priority: 3, CreatedAt: base}
		if !issuePriorityBefore(a, b) || issuePriorityBefore(b, a) {
			t.Fatal("expected P0 to sort before P3")
		}
	})

	t.Run("same priority: newer created sorts first", func(t *testing.T) {
		t.Parallel()
		older := &types.Issue{ID: "a", Priority: 1, CreatedAt: base}
		newer := &types.Issue{ID: "b", Priority: 1, CreatedAt: later}
		if !issuePriorityBefore(newer, older) {
			t.Fatal("expected newer issue to sort before older at same priority")
		}
	})

	t.Run("same priority and time: id breaks tie", func(t *testing.T) {
		t.Parallel()
		a := &types.Issue{ID: "a", Priority: 1, CreatedAt: base}
		b := &types.Issue{ID: "b", Priority: 1, CreatedAt: base}
		if !issuePriorityBefore(a, b) || issuePriorityBefore(b, a) {
			t.Fatal("expected id to break the tie (a before b)")
		}
	})
}

func TestIssueCreatedBefore(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	later := base.Add(time.Hour)

	t.Run("older created sorts first", func(t *testing.T) {
		t.Parallel()
		older := &types.Issue{ID: "a", CreatedAt: base}
		newer := &types.Issue{ID: "b", CreatedAt: later}
		if !issueCreatedBefore(older, newer) || issueCreatedBefore(newer, older) {
			t.Fatal("expected older issue to sort first")
		}
	})

	t.Run("same time: id breaks tie", func(t *testing.T) {
		t.Parallel()
		a := &types.Issue{ID: "a", CreatedAt: base}
		b := &types.Issue{ID: "b", CreatedAt: base}
		if !issueCreatedBefore(a, b) {
			t.Fatal("expected id to break the tie (a before b)")
		}
	})
}

func TestSortReadyIssues(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	// Recent = within 48h; old = well before the cutoff.
	recentHigh := &types.Issue{ID: "recent-p0", Priority: 0, CreatedAt: now.Add(-1 * time.Hour)}
	recentLow := &types.Issue{ID: "recent-p3", Priority: 3, CreatedAt: now.Add(-2 * time.Hour)}
	oldHigh := &types.Issue{ID: "old-p0", Priority: 0, CreatedAt: now.Add(-100 * time.Hour)}

	ids := func(issues []*types.Issue) []string {
		out := make([]string, len(issues))
		for i, is := range issues {
			out[i] = is.ID
		}
		return out
	}

	t.Run("priority policy orders by priority asc", func(t *testing.T) {
		t.Parallel()
		issues := []*types.Issue{recentLow, oldHigh, recentHigh}
		sortReadyIssues(issues, types.SortPolicyPriority)
		// P0s first (both recentHigh and oldHigh), then P3.
		got := ids(issues)
		if got[len(got)-1] != "recent-p3" {
			t.Fatalf("priority policy = %v, want P3 last", got)
		}
	})

	t.Run("oldest policy orders by created asc", func(t *testing.T) {
		t.Parallel()
		issues := []*types.Issue{recentHigh, oldHigh, recentLow}
		sortReadyIssues(issues, types.SortPolicyOldest)
		got := ids(issues)
		if got[0] != "old-p0" {
			t.Fatalf("oldest policy = %v, want old-p0 first", got)
		}
	})

	t.Run("hybrid policy prefers recent issues over old ones", func(t *testing.T) {
		t.Parallel()
		issues := []*types.Issue{oldHigh, recentLow, recentHigh}
		sortReadyIssues(issues, types.SortPolicyHybrid)
		got := ids(issues)
		// Both recent issues must precede the old one despite old-p0's high priority.
		if got[len(got)-1] != "old-p0" {
			t.Fatalf("hybrid policy = %v, want old-p0 last (recency beats priority)", got)
		}
		// Among recent, higher priority (recent-p0) precedes recent-p3.
		if got[0] != "recent-p0" {
			t.Fatalf("hybrid policy = %v, want recent-p0 first", got)
		}
	})

	t.Run("empty policy string behaves like hybrid", func(t *testing.T) {
		t.Parallel()
		issues := []*types.Issue{oldHigh, recentHigh}
		sortReadyIssues(issues, "")
		if ids(issues)[0] != "recent-p0" {
			t.Fatalf("empty policy = %v, want recent-p0 first (hybrid default)", ids(issues))
		}
	})
}
