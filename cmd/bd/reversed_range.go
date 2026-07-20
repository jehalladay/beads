package main

import (
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// reversedRangeMessage returns an error message describing the first reversed
// (min > max / after > before) range in an IssueFilter, or "" if every range
// is ordered or unset. A reversed range builds an always-false WHERE
// ("priority >= 4 AND priority <= 0", "created_at >= 2099 AND created_at <=
// 2020"), so the query silently returns a zero/empty result instead of
// surfacing the contradiction. Equal bounds (min==max, after==before) are
// valid. Shared by bd count and bd search (beads-8a631); parity with the bd
// list guard (beads-wnm6g/BUG-36/BUG-37) and the bd ready guard (beads-tjysi).
func reversedRangeMessage(f types.IssueFilter) string {
	if f.PriorityMin != nil && f.PriorityMax != nil && *f.PriorityMin > *f.PriorityMax {
		return fmt.Sprintf("--priority-min (%d) cannot be greater than --priority-max (%d)", *f.PriorityMin, *f.PriorityMax)
	}
	for _, axis := range []struct {
		name          string
		after, before *time.Time
	}{
		{"created", f.CreatedAfter, f.CreatedBefore},
		{"updated", f.UpdatedAfter, f.UpdatedBefore},
		{"closed", f.ClosedAfter, f.ClosedBefore},
	} {
		if axis.after != nil && axis.before != nil && axis.after.After(*axis.before) {
			return fmt.Sprintf("--%s-after (%s) cannot be later than --%s-before (%s)",
				axis.name, axis.after.Format("2006-01-02"), axis.name, axis.before.Format("2006-01-02"))
		}
	}
	return ""
}
