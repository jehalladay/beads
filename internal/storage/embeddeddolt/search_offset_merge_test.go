//go:build cgo

package embeddeddolt_test

import (
	"fmt"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestSearchOffsetMergeEdges pins beads-cand's offset correctness on the
// issues+wisps merge path, including the edge where one half is shorter than
// the offset (each half over-fetches Offset+Limit, so the offset window is
// served from whichever half(s) it lands in). Gated on embedded dolt.
func TestSearchOffsetMergeEdges(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "somE")
	ctx := t.Context()
	// 2 durable issues + 5 NoHistory wisps; id-sorted: i1,i2,w1,w2,w3,w4,w5.
	for i := 1; i <= 2; i++ {
		if err := te.store.CreateIssue(ctx, &types.Issue{ID: fmt.Sprintf("somE-i%d", i), Title: "i", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeBug}, "t"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 5; i++ {
		if err := te.store.CreateIssue(ctx, &types.Issue{ID: fmt.Sprintf("somE-w%d", i), Title: "w", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeBug, NoHistory: true}, "t"); err != nil {
			t.Fatal(err)
		}
	}
	page := func(off, lim int) []string {
		r, err := te.store.SearchIssues(ctx, "", types.IssueFilter{SortBy: "id", Limit: lim, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(r))
		for i, x := range r {
			out[i] = x.ID
		}
		return out
	}
	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	cases := []struct {
		name          string
		offset, limit int
		want          []string
	}{
		{"spans issues->wisps boundary", 1, 2, []string{"somE-i2", "somE-w1"}},
		{"exactly past issues half", 2, 2, []string{"somE-w1", "somE-w2"}},
		{"offset into wisps (issues half < offset)", 3, 2, []string{"somE-w2", "somE-w3"}},
		{"last partial page", 6, 2, []string{"somE-w5"}},
		{"offset at total end", 7, 2, nil},
		{"offset past total", 99, 2, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := page(c.offset, c.limit); !eq(got, c.want) {
				t.Errorf("offset=%d limit=%d = %v, want %v", c.offset, c.limit, got, c.want)
			}
		})
	}
}
