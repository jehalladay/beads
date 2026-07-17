package notion

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	itracker "github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// searchStore is a storage.Storage whose SearchIssues returns a canned result
// (or error); every other method comes from the embedded nil interface and
// panics if the code under test unexpectedly reaches it.
type searchStore struct {
	storage.Storage
	issues []*types.Issue
	err    error
}

func (s *searchStore) SearchIssues(_ context.Context, _ string, _ types.IssueFilter) ([]*types.Issue, error) {
	return s.issues, s.err
}

func TestBuildLocalPullIndexes(t *testing.T) {
	ctx := context.Background()

	t.Run("nil store returns empty maps", func(t *testing.T) {
		tr := &Tracker{}
		byExt, byID, err := tr.buildLocalPullIndexes(ctx)
		if err != nil {
			t.Fatalf("nil store = err %v, want nil", err)
		}
		if len(byExt) != 0 || len(byID) != 0 {
			t.Fatalf("nil store = %d ext / %d id, want empty", len(byExt), len(byID))
		}
	})

	t.Run("store error propagates", func(t *testing.T) {
		tr := &Tracker{store: &searchStore{err: errors.New("boom")}}
		if _, _, err := tr.buildLocalPullIndexes(ctx); err == nil {
			t.Fatal("store error = nil, want wrapped error")
		}
	})

	t.Run("indexes by id and external identifier", func(t *testing.T) {
		notionURL := "https://www.notion.so/12345678123412341234123456789abc"
		tr := &Tracker{store: &searchStore{issues: []*types.Issue{
			nil, // nil issue skipped
			{ID: "bd-1", ExternalRef: strPtr(notionURL)},
			{ID: "  ", ExternalRef: nil},          // blank id + nil ref: neither indexed
			{ID: "bd-2", ExternalRef: strPtr("")}, // empty ref: id indexed, no ext
		}}}
		byExt, byID, err := tr.buildLocalPullIndexes(ctx)
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		if _, ok := byID["bd-1"]; !ok {
			t.Error("bd-1 not indexed by id")
		}
		if _, ok := byID["bd-2"]; !ok {
			t.Error("bd-2 not indexed by id")
		}
		if len(byID) != 2 {
			t.Errorf("byID has %d entries, want 2 (blank id skipped)", len(byID))
		}
		want := ExtractNotionIdentifier(notionURL)
		if want == "" {
			t.Fatal("test fixture URL produced no identifier")
		}
		if _, ok := byExt[want]; !ok {
			t.Errorf("external identifier %q not indexed", want)
		}
		if len(byExt) != 1 {
			t.Errorf("byExt has %d entries, want 1", len(byExt))
		}
	})
}

func TestTrackerIssueEqual(t *testing.T) {
	base := func() (*types.Issue, *itracker.TrackerIssue) {
		local := &types.Issue{
			Title:       "Hi",
			Description: "Body",
			Priority:    2,
			Status:      types.StatusOpen,
			IssueType:   types.TypeTask,
			Assignee:    "alice",
			Labels:      []string{"a", "b"},
		}
		remote := &itracker.TrackerIssue{
			Title:       "Hi",
			Description: "Body",
			Priority:    2,
			State:       types.StatusOpen,
			Type:        types.TypeTask,
			Assignee:    "alice",
			Labels:      []string{"b", "a"},
		}
		return local, remote
	}

	t.Run("equal ignores label order and whitespace", func(t *testing.T) {
		l, r := base()
		l.Title = "  Hi  "
		if !trackerIssueEqual(l, r) {
			t.Error("expected equal")
		}
	})

	nilCases := []struct {
		name  string
		local *types.Issue
		rem   *itracker.TrackerIssue
	}{
		{"nil local", nil, &itracker.TrackerIssue{}},
		{"nil remote", &types.Issue{}, nil},
	}
	for _, tc := range nilCases {
		t.Run(tc.name, func(t *testing.T) {
			if trackerIssueEqual(tc.local, tc.rem) {
				t.Error("nil operand = true, want false")
			}
		})
	}

	diffCases := []struct {
		name   string
		mutate func(*types.Issue, *itracker.TrackerIssue)
	}{
		{"title differs", func(l *types.Issue, _ *itracker.TrackerIssue) { l.Title = "X" }},
		{"description differs", func(l *types.Issue, _ *itracker.TrackerIssue) { l.Description = "X" }},
		{"priority differs", func(l *types.Issue, _ *itracker.TrackerIssue) { l.Priority = 9 }},
		{"state type mismatch", func(_ *types.Issue, r *itracker.TrackerIssue) { r.State = "not-a-status" }},
		{"state value differs", func(_ *types.Issue, r *itracker.TrackerIssue) { r.State = types.StatusClosed }},
		{"type mismatch", func(_ *types.Issue, r *itracker.TrackerIssue) { r.Type = "not-a-type" }},
		{"type value differs", func(_ *types.Issue, r *itracker.TrackerIssue) { r.Type = types.TypeBug }},
		{"assignee differs", func(l *types.Issue, _ *itracker.TrackerIssue) { l.Assignee = "bob" }},
		{"labels differ", func(l *types.Issue, _ *itracker.TrackerIssue) { l.Labels = []string{"only"} }},
	}
	for _, tc := range diffCases {
		t.Run(tc.name, func(t *testing.T) {
			l, r := base()
			tc.mutate(l, r)
			if trackerIssueEqual(l, r) {
				t.Errorf("%s = equal, want not equal", tc.name)
			}
		})
	}
}

func TestEqualStringSets(t *testing.T) {
	cases := []struct {
		name  string
		left  []string
		right []string
		want  bool
	}{
		{"length differs", []string{"a"}, []string{"a", "b"}, false},
		{"same after normalize", []string{" a ", "b"}, []string{"b", "a"}, true},
		// Length is checked pre-normalization, then only the normalized left
		// slice is iterated: a blank on the left shrinks it to a prefix of the
		// right, so this compares equal (documents actual behavior).
		{"blank left shrinks to matching prefix", []string{"a", ""}, []string{"a", "b"}, true},
		{"element differs", []string{"a", "c"}, []string{"a", "b"}, false},
		{"both empty", nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := equalStringSets(tc.left, tc.right); got != tc.want {
				t.Errorf("equalStringSets(%v,%v) = %v, want %v", tc.left, tc.right, got, tc.want)
			}
		})
	}
}

func TestSameTrackerIssue(t *testing.T) {
	url := "https://www.notion.so/12345678123412341234123456789abc"
	id := ExtractNotionIdentifier(url)
	if id == "" {
		t.Fatal("fixture produced no identifier")
	}

	t.Run("match on shared identifier across fields", func(t *testing.T) {
		left := itracker.TrackerIssue{URL: url}
		right := itracker.TrackerIssue{ID: url}
		if !sameTrackerIssue(left, right) {
			t.Error("expected match on shared notion id")
		}
	})

	t.Run("no match when all identifiers empty", func(t *testing.T) {
		if sameTrackerIssue(itracker.TrackerIssue{}, itracker.TrackerIssue{}) {
			t.Error("empty issues = match, want no match")
		}
	})

	t.Run("no match on distinct ids", func(t *testing.T) {
		other := "https://www.notion.so/abcdef01abcdef01abcdef01abcdef01"
		left := itracker.TrackerIssue{URL: url}
		right := itracker.TrackerIssue{URL: other}
		if sameTrackerIssue(left, right) {
			t.Error("distinct ids = match, want no match")
		}
	})
}

func TestMatchesFetchState(t *testing.T) {
	open := &itracker.TrackerIssue{State: types.StatusOpen}
	closed := &itracker.TrackerIssue{State: types.StatusClosed}

	cases := []struct {
		name   string
		issue  *itracker.TrackerIssue
		filter string
		want   bool
	}{
		{"nil issue", nil, "open", false},
		{"empty filter matches all", open, "", true},
		{"all filter", closed, "ALL", true},
		{"open matches open", open, "open", true},
		{"open rejects closed", closed, "open", false},
		{"closed matches closed", closed, "closed", true},
		{"closed rejects open", open, "closed", false},
		{"unknown filter matches", open, "weird", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesFetchState(tc.issue, tc.filter); got != tc.want {
				t.Errorf("matchesFetchState(%q) = %v, want %v", tc.filter, got, tc.want)
			}
		})
	}
}

func TestShouldBackfillNotionIssue(t *testing.T) {
	url := "https://www.notion.so/12345678123412341234123456789abc"
	ident := ExtractNotionIdentifier(url)
	if ident == "" {
		t.Fatal("fixture produced no identifier")
	}

	t.Run("nil issue", func(t *testing.T) {
		if shouldBackfillNotionIssue(nil, nil, nil) {
			t.Error("nil issue = true, want false")
		}
	})

	t.Run("already present by external identifier", func(t *testing.T) {
		issue := &itracker.TrackerIssue{URL: url}
		byExt := map[string]struct{}{ident: {}}
		if shouldBackfillNotionIssue(issue, byExt, nil) {
			t.Error("known external id = backfill, want skip")
		}
	})

	t.Run("raw not a PulledIssue", func(t *testing.T) {
		issue := &itracker.TrackerIssue{URL: url, Raw: "not-a-pulled-issue"}
		if shouldBackfillNotionIssue(issue, map[string]struct{}{}, map[string]struct{}{}) {
			t.Error("non-PulledIssue raw = backfill, want skip")
		}
	})

	t.Run("raw with blank local id", func(t *testing.T) {
		issue := &itracker.TrackerIssue{URL: url, Raw: &PulledIssue{ID: "  "}}
		if shouldBackfillNotionIssue(issue, map[string]struct{}{}, map[string]struct{}{}) {
			t.Error("blank local id = backfill, want skip")
		}
	})

	t.Run("local id already present", func(t *testing.T) {
		issue := &itracker.TrackerIssue{URL: url, Raw: &PulledIssue{ID: "bd-9"}}
		byID := map[string]struct{}{"bd-9": {}}
		if shouldBackfillNotionIssue(issue, map[string]struct{}{}, byID) {
			t.Error("known local id = backfill, want skip")
		}
	})

	t.Run("new issue should backfill", func(t *testing.T) {
		issue := &itracker.TrackerIssue{URL: url, Raw: &PulledIssue{ID: "bd-new"}}
		if !shouldBackfillNotionIssue(issue, map[string]struct{}{}, map[string]struct{}{}) {
			t.Error("new issue = skip, want backfill")
		}
	})
}

func TestPriorityAndStatusToNotionFallbacks(t *testing.T) {
	cfg := &MappingConfig{
		PriorityToNotion: map[int]string{1: "High"},
		StatusToNotion:   map[types.Status]string{types.StatusOpen: "Todo"},
	}

	if got := priorityToNotion(1, cfg); got != "High" {
		t.Errorf("priorityToNotion(1) = %q, want High", got)
	}
	if got := priorityToNotion(99, cfg); got != "Medium" {
		t.Errorf("priorityToNotion(unknown) = %q, want Medium fallback", got)
	}
	if got := statusToNotion(types.StatusOpen, cfg); got != "Todo" {
		t.Errorf("statusToNotion(open) = %q, want Todo", got)
	}
	if got := statusToNotion(types.StatusClosed, cfg); got != "Open" {
		t.Errorf("statusToNotion(unknown) = %q, want Open fallback", got)
	}
}

func TestFirstNonEmptyAllEmpty(t *testing.T) {
	if got := firstNonEmpty("", "   ", "\t"); got != "" {
		t.Errorf("firstNonEmpty(all blank) = %q, want empty", got)
	}
	if got := firstNonEmpty("", "found", "later"); got != "found" {
		t.Errorf("firstNonEmpty = %q, want found", got)
	}
}
