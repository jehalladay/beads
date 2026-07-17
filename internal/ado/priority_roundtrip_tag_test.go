package ado

import (
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// TestPriorityRoundTrip_ViaTag is the regression for beads-yfvg: a beads
// priority 3 or 4 must survive a REAL push→ADO→pull round-trip, where ADO only
// echoes back the fields it was actually sent (priority + tags). The old
// beads_priority metadata channel was never sent to ADO, so priority 4 silently
// decayed to 3 on pull. The fix carries the exact priority as a beads:priority:N
// tag, which does round-trip through FieldTags.
func TestPriorityRoundTrip_ViaTag(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	for _, tc := range []struct {
		beadsPriority int
		wantADO       int
		wantTag       string
	}{
		{3, 4, "beads:priority:3"},
		{4, 4, "beads:priority:4"},
	} {
		t.Run(tc.wantTag, func(t *testing.T) {
			// Push: beads → ADO fields.
			issue := &types.Issue{Title: "T", Priority: tc.beadsPriority, Status: types.StatusOpen, IssueType: types.TypeTask}
			fields := m.IssueToTracker(issue)

			if fields[FieldPriority] != tc.wantADO {
				t.Fatalf("push ADO priority = %v, want %d", fields[FieldPriority], tc.wantADO)
			}
			// The exact priority must ride along as a tag (the channel that reaches ADO).
			tagStr, _ := fields[FieldTags].(string)
			if !hasBeadsTag(tagStr, tc.wantTag) {
				t.Fatalf("push tags = %q, want to contain %q", tagStr, tc.wantTag)
			}

			// Pull: reconstruct a WorkItem from ONLY what ADO stores/echoes back —
			// the pushed priority and tags. Crucially, ti.Metadata carries NO
			// beads_priority (a real ADO pull never sets it; see tracker.go).
			wi := &WorkItem{
				ID:  50,
				URL: "https://dev.azure.com/o/p/_apis/wit/workItems/50",
				Fields: map[string]interface{}{
					FieldTitle:        "T",
					FieldState:        "New",
					FieldPriority:     float64(tc.wantADO),
					FieldWorkItemType: "Task",
					FieldTags:         tagStr,
				},
			}
			conv := m.IssueToBeads(&tracker.TrackerIssue{ID: "50", Raw: wi})
			if conv == nil {
				t.Fatal("pull: IssueToBeads returned nil")
			}
			if conv.Issue.Priority != tc.beadsPriority {
				t.Errorf("round-trip priority = %d, want %d (decayed via lossy ADO mapping)", conv.Issue.Priority, tc.beadsPriority)
			}
			// The internal priority tag must not leak into user-visible labels.
			for _, l := range conv.Issue.Labels {
				if l == tc.wantTag {
					t.Errorf("priority tag %q leaked into labels %v", tc.wantTag, conv.Issue.Labels)
				}
			}
		})
	}
}

// TestPriorityFromTags covers the tag parser directly across present, absent,
// and malformed/out-of-range inputs.
func TestPriorityFromTags(t *testing.T) {
	cases := []struct {
		name   string
		tags   []string
		want   int
		wantOK bool
	}{
		{"present valid", []string{"a", "beads:priority:4", "b"}, 4, true},
		{"present zero", []string{"beads:priority:0"}, 0, true},
		{"absent", []string{"a", "beads:blocked"}, 0, false},
		{"empty", nil, 0, false},
		{"non-numeric", []string{"beads:priority:x"}, 0, false},
		{"out of range high", []string{"beads:priority:9"}, 0, false},
		{"out of range negative", []string{"beads:priority:-1"}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := priorityFromTags(tc.tags)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("priorityFromTags(%v) = (%d,%v), want (%d,%v)", tc.tags, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
