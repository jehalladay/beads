package issueops

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func mkIssues(ids ...string) []*types.Issue {
	out := make([]*types.Issue, len(ids))
	for i, id := range ids {
		out[i] = &types.Issue{ID: id}
	}
	return out
}

// TestApplyOffsetLimit pins the beads-cand pagination contract: Offset then
// Limit over an already-sorted slice, with offset-past-end yielding empty.
func TestApplyOffsetLimit(t *testing.T) {
	ids := func(is []*types.Issue) []string {
		o := make([]string, len(is))
		for i, x := range is {
			o[i] = x.ID
		}
		return o
	}
	eq := func(got []*types.Issue, want ...string) bool {
		g := ids(got)
		if len(g) != len(want) {
			return false
		}
		for i := range g {
			if g[i] != want[i] {
				return false
			}
		}
		return true
	}
	base := mkIssues("a", "b", "c", "d", "e")

	cases := []struct {
		name          string
		offset, limit int
		want          []string
	}{
		{"no offset no limit", 0, 0, []string{"a", "b", "c", "d", "e"}},
		{"limit only", 0, 2, []string{"a", "b"}},
		{"offset only", 2, 0, []string{"c", "d", "e"}},
		{"offset+limit", 1, 2, []string{"b", "c"}},
		{"offset to last partial page", 4, 2, []string{"e"}},
		{"offset exactly at end", 5, 2, nil},
		{"offset past end", 99, 2, nil},
		{"negative offset ignored", -1, 2, []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := applyOffsetLimit(base, types.IssueFilter{Offset: c.offset, Limit: c.limit})
			if !eq(got, c.want...) {
				t.Errorf("applyOffsetLimit(offset=%d,limit=%d) = %v, want %v", c.offset, c.limit, ids(got), c.want)
			}
		})
	}
}

// TestEffectiveFetchLimit: each per-table query must over-fetch Offset+Limit so
// the post-merge offset window is complete; unbounded (Limit<=0) stays 0.
func TestEffectiveFetchLimit(t *testing.T) {
	cases := []struct {
		offset, limit, want int
	}{
		{0, 0, 0},   // unbounded
		{5, 0, 0},   // offset without limit → still unbounded fetch
		{0, 10, 10}, // no offset
		{20, 10, 30},
	}
	for _, c := range cases {
		if got := effectiveFetchLimit(types.IssueFilter{Offset: c.offset, Limit: c.limit}); got != c.want {
			t.Errorf("effectiveFetchLimit(offset=%d,limit=%d) = %d, want %d", c.offset, c.limit, got, c.want)
		}
	}
}
