// Table-driven unit tests for the pure/near-pure engine helpers that carried
// 0% coverage (beads-3f0, C1 agentic-tdd under beads-r06). These exercise the
// normalization helpers, firstNonEmpty, formatPushIssue, collectBatchPushIssues,
// shouldPushIssue, and renderBatchDryRun without CGO or network, reusing the
// pure-Go mockTracker/pureTestStore harness in test_helpers_pure_test.go.

package tracker

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestNormalizedStringSet(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want map[string]struct{}
	}{
		{"nil", nil, map[string]struct{}{}},
		{"empty slice", []string{}, map[string]struct{}{}},
		{"blank and whitespace dropped", []string{"", "   ", "\t"}, map[string]struct{}{}},
		{"trims surrounding space", []string{"  a  ", "b"}, map[string]struct{}{"a": {}, "b": {}}},
		{"dedupes after trim", []string{"a", " a ", "a"}, map[string]struct{}{"a": {}}},
		{"mixed", []string{" x ", "", "y", "y"}, map[string]struct{}{"x": {}, "y": {}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizedStringSet(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("normalizedStringSet(%v) size = %d, want %d (%v)", tt.in, len(got), len(tt.want), got)
			}
			for k := range tt.want {
				if _, ok := got[k]; !ok {
					t.Errorf("normalizedStringSet(%v) missing key %q", tt.in, k)
				}
			}
		})
	}
}

func TestNormalizedStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"sorted output", []string{"c", "a", "b"}, []string{"a", "b", "c"}},
		{"trim + dedupe + sort", []string{" b ", "a", "b", ""}, []string{"a", "b"}},
		{"all blank", []string{"", "  "}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizedStringSlice(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("normalizedStringSlice(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("normalizedStringSlice(%v)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestEqualNormalizedStrings(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs empty", nil, []string{}, true},
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"b", "a"}, []string{"a", "b"}, true},
		{"whitespace-insensitive", []string{" a ", "b"}, []string{"a", " b "}, true},
		{"dedupe makes equal", []string{"a", "a", "b"}, []string{"a", "b"}, true},
		{"blanks ignored", []string{"a", "", "b"}, []string{"a", "b"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different content", []string{"a", "c"}, []string{"a", "b"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := equalNormalizedStrings(tt.a, tt.b); got != tt.want {
				t.Errorf("equalNormalizedStrings(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"no args", nil, ""},
		{"all empty", []string{"", "  ", "\t"}, ""},
		{"first wins", []string{"a", "b"}, "a"},
		{"skips leading blanks", []string{"", "  ", "x", "y"}, "x"},
		{"returns original (untrimmed) value", []string{"  padded  "}, "  padded  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstNonEmpty(tt.in...); got != tt.want {
				t.Errorf("firstNonEmpty(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEngineFormatPushIssue(t *testing.T) {
	issue := &types.Issue{ID: "bd-1", Title: "t", Description: "orig"}

	t.Run("nil hooks returns same pointer", func(t *testing.T) {
		e := &Engine{}
		got := e.formatPushIssue(issue)
		if got != issue {
			t.Errorf("expected same pointer when PushHooks nil")
		}
	})

	t.Run("nil FormatDescription returns same pointer", func(t *testing.T) {
		e := &Engine{PushHooks: &PushHooks{}}
		got := e.formatPushIssue(issue)
		if got != issue {
			t.Errorf("expected same pointer when FormatDescription nil")
		}
	})

	t.Run("formats into a copy without mutating original", func(t *testing.T) {
		e := &Engine{PushHooks: &PushHooks{
			FormatDescription: func(i *types.Issue) string { return "formatted:" + i.Description },
		}}
		got := e.formatPushIssue(issue)
		if got == issue {
			t.Fatalf("expected a distinct copy, got same pointer")
		}
		if got.Description != "formatted:orig" {
			t.Errorf("copy.Description = %q, want %q", got.Description, "formatted:orig")
		}
		if issue.Description != "orig" {
			t.Errorf("original mutated: Description = %q, want %q", issue.Description, "orig")
		}
		if got.ID != issue.ID || got.Title != issue.Title {
			t.Errorf("copy lost fields: got ID=%q Title=%q", got.ID, got.Title)
		}
	})
}

func TestEngineCollectBatchPushIssues(t *testing.T) {
	str := func(s string) *string { return &s }

	// isRef treats refs beginning with "EXT-" as existing external refs so we
	// can drive the CreateOnly branch (willCreate == false).
	tracker := &mockExternalRefTracker{
		mockTracker: newMockTracker("test"),
		isRef:       func(ref string) bool { return len(ref) >= 4 && ref[:4] == "EXT-" },
	}

	issues := []*types.Issue{
		{ID: "a", Ephemeral: false},
		{ID: "b", Ephemeral: true},           // filtered by ExcludeEphemeral
		{ID: "c", ExternalRef: str("EXT-c")}, // existing ref: skipped under CreateOnly
		{ID: "d", ExternalRef: str("")},      // no ref: create candidate
		{ID: "e", ExternalRef: str("EXT-e")}, // existing ref: skipped under CreateOnly unless forced
	}

	tests := []struct {
		name          string
		opts          SyncOptions
		descendantSet map[string]bool
		skipIDs       map[string]bool
		forceIDs      map[string]bool
		wantIDs       []string
		wantSkipped   int
	}{
		{
			name:        "no filters keeps all",
			wantIDs:     []string{"a", "b", "c", "d", "e"},
			wantSkipped: 0,
		},
		{
			name:        "ExcludeEphemeral drops b",
			opts:        SyncOptions{ExcludeEphemeral: true},
			wantIDs:     []string{"a", "c", "d", "e"},
			wantSkipped: 1,
		},
		{
			name:          "descendantSet restricts membership",
			descendantSet: map[string]bool{"a": true, "d": true},
			wantIDs:       []string{"a", "d"},
			wantSkipped:   3,
		},
		{
			name:        "skipIDs removes explicit ids",
			skipIDs:     map[string]bool{"a": true},
			wantIDs:     []string{"b", "c", "d", "e"},
			wantSkipped: 1,
		},
		{
			name:        "CreateOnly skips issues with existing external refs",
			opts:        SyncOptions{CreateOnly: true},
			wantIDs:     []string{"a", "b", "d"},
			wantSkipped: 2,
		},
		{
			name:        "CreateOnly honors forceIDs",
			opts:        SyncOptions{CreateOnly: true},
			forceIDs:    map[string]bool{"c": true},
			wantIDs:     []string{"a", "b", "c", "d"},
			wantSkipped: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Engine{Tracker: tracker}
			got, skipped := e.collectBatchPushIssues(issues, tt.opts, tt.descendantSet, tt.skipIDs, tt.forceIDs)
			if skipped != tt.wantSkipped {
				t.Errorf("skipped = %d, want %d", skipped, tt.wantSkipped)
			}
			gotIDs := make([]string, len(got))
			for i, iss := range got {
				gotIDs[i] = iss.ID
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("pushIssues = %v, want %v", gotIDs, tt.wantIDs)
			}
			for i := range tt.wantIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Errorf("pushIssues[%d] = %q, want %q (%v)", i, gotIDs[i], tt.wantIDs[i], gotIDs)
				}
			}
		})
	}
}

func TestEngineCollectBatchPushIssuesShouldPushHook(t *testing.T) {
	tracker := newMockTracker("test")
	issues := []*types.Issue{{ID: "keep"}, {ID: "drop"}}
	e := &Engine{
		Tracker: tracker,
		PushHooks: &PushHooks{
			ShouldPush: func(i *types.Issue) bool { return i.ID != "drop" },
		},
	}
	got, skipped := e.collectBatchPushIssues(issues, SyncOptions{}, nil, nil, nil)
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(got) != 1 || got[0].ID != "keep" {
		t.Fatalf("pushIssues = %v, want [keep]", got)
	}
}

func TestEngineShouldPushIssue(t *testing.T) {
	e := &Engine{}
	tests := []struct {
		name  string
		issue *types.Issue
		opts  SyncOptions
		want  bool
	}{
		{"default keeps", &types.Issue{IssueType: types.TypeTask}, SyncOptions{}, true},
		{"ExcludeEphemeral drops ephemeral", &types.Issue{Ephemeral: true}, SyncOptions{ExcludeEphemeral: true}, false},
		{"ExcludeEphemeral keeps non-ephemeral", &types.Issue{Ephemeral: false}, SyncOptions{ExcludeEphemeral: true}, true},
		{"TypeFilter match", &types.Issue{IssueType: types.TypeBug}, SyncOptions{TypeFilter: []types.IssueType{types.TypeBug}}, true},
		{"TypeFilter miss", &types.Issue{IssueType: types.TypeTask}, SyncOptions{TypeFilter: []types.IssueType{types.TypeBug}}, false},
		{"ExcludeTypes drops match", &types.Issue{IssueType: types.TypeBug}, SyncOptions{ExcludeTypes: []types.IssueType{types.TypeBug}}, false},
		{"State open drops closed", &types.Issue{Status: types.StatusClosed}, SyncOptions{State: "open"}, false},
		{"State open keeps open", &types.Issue{Status: types.StatusOpen}, SyncOptions{State: "open"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.shouldPushIssue(tt.issue, tt.opts); got != tt.want {
				t.Errorf("shouldPushIssue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEngineRenderBatchDryRun(t *testing.T) {
	t.Run("nil result is a no-op", func(t *testing.T) {
		e := &Engine{Tracker: newMockTracker("test")}
		e.renderBatchDryRun(nil, nil) // must not panic
	})

	t.Run("emits create/update lines with resolved titles", func(t *testing.T) {
		var msgs []string
		e := &Engine{
			Tracker:   newMockTracker("test"),
			OnMessage: func(m string) { msgs = append(msgs, m) },
		}
		issues := []*types.Issue{
			{ID: "bd-1", Title: "First"},
			{ID: "bd-2", Title: "Second"},
			nil,                      // skipped defensively
			{ID: "", Title: "no-id"}, // skipped: empty ID
		}
		result := &BatchPushResult{
			Created: []BatchPushItem{{LocalID: "bd-1"}},
			Updated: []BatchPushItem{{LocalID: "bd-2"}},
		}
		e.renderBatchDryRun(issues, result)
		if len(msgs) != 2 {
			t.Fatalf("emitted %d messages, want 2: %v", len(msgs), msgs)
		}
		if !strings.Contains(msgs[0], "First") || !strings.Contains(msgs[0], "create") {
			t.Errorf("create line = %q, want create+First", msgs[0])
		}
		if !strings.Contains(msgs[1], "Second") || !strings.Contains(msgs[1], "update") {
			t.Errorf("update line = %q, want update+Second", msgs[1])
		}
	})
}
