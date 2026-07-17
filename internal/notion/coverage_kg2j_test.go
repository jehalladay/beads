package notion

import (
	"context"
	"testing"

	itracker "github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

func TestBuildExternalRef_Branches(t *testing.T) {
	t.Parallel()
	tr := &Tracker{}
	const pageID = "0123456789abcdef0123456789abcdef"
	const canonical = "https://www.notion.so/" + pageID

	t.Run("nil issue -> empty", func(t *testing.T) {
		t.Parallel()
		if got := tr.BuildExternalRef(nil); got != "" {
			t.Fatalf("nil issue = %q, want empty", got)
		}
	})

	t.Run("URL field is canonicalized first", func(t *testing.T) {
		t.Parallel()
		got := tr.BuildExternalRef(&itracker.TrackerIssue{URL: "https://notion.so/Page-" + pageID})
		if got != canonical {
			t.Fatalf("from URL = %q, want %q", got, canonical)
		}
	})

	t.Run("falls back to Identifier when URL and ID empty", func(t *testing.T) {
		t.Parallel()
		got := tr.BuildExternalRef(&itracker.TrackerIssue{Identifier: canonical})
		if got != canonical {
			t.Fatalf("from Identifier = %q, want %q", got, canonical)
		}
	})

	t.Run("all fields non-canonical -> empty", func(t *testing.T) {
		t.Parallel()
		got := tr.BuildExternalRef(&itracker.TrackerIssue{URL: "not-a-page", ID: "nope", Identifier: "nada"})
		if got != "" {
			t.Fatalf("no canonical field = %q, want empty", got)
		}
	})
}

func TestTrackerIssueEqual_Branches(t *testing.T) {
	t.Parallel()

	base := func() (*types.Issue, *itracker.TrackerIssue) {
		local := &types.Issue{
			Title:       "Title",
			Description: "Desc",
			Priority:    2,
			Status:      types.StatusOpen,
			IssueType:   types.TypeTask,
			Assignee:    "alice",
			Labels:      []string{"a", "b"},
		}
		remote := &itracker.TrackerIssue{
			Title:       "Title",
			Description: "Desc",
			Priority:    2,
			State:       types.StatusOpen,
			Type:        types.TypeTask,
			Assignee:    "alice",
			Labels:      []string{"b", "a"},
		}
		return local, remote
	}

	t.Run("equal (labels order-insensitive, whitespace-trimmed)", func(t *testing.T) {
		t.Parallel()
		l, r := base()
		l.Title = "  Title  " // trimmed before compare
		if !trackerIssueEqual(l, r) {
			t.Fatal("expected equal")
		}
	})

	t.Run("nil local or remote -> not equal", func(t *testing.T) {
		t.Parallel()
		if trackerIssueEqual(nil, &itracker.TrackerIssue{}) {
			t.Fatal("nil local should not be equal")
		}
		if trackerIssueEqual(&types.Issue{}, nil) {
			t.Fatal("nil remote should not be equal")
		}
	})

	cases := []struct {
		name   string
		mutate func(l *types.Issue, r *itracker.TrackerIssue)
	}{
		{"title differs", func(l *types.Issue, r *itracker.TrackerIssue) { r.Title = "Other" }},
		{"description differs", func(l *types.Issue, r *itracker.TrackerIssue) { r.Description = "Other" }},
		{"priority differs", func(l *types.Issue, r *itracker.TrackerIssue) { r.Priority = 4 }},
		{"state type mismatch", func(l *types.Issue, r *itracker.TrackerIssue) { r.State = "open" }},
		{"state value differs", func(l *types.Issue, r *itracker.TrackerIssue) { r.State = types.StatusClosed }},
		{"type mismatch", func(l *types.Issue, r *itracker.TrackerIssue) { r.Type = "task" }},
		{"type value differs", func(l *types.Issue, r *itracker.TrackerIssue) { r.Type = types.TypeBug }},
		{"assignee differs", func(l *types.Issue, r *itracker.TrackerIssue) { r.Assignee = "bob" }},
		{"labels differ", func(l *types.Issue, r *itracker.TrackerIssue) { r.Labels = []string{"x"} }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name+" -> not equal", func(t *testing.T) {
			t.Parallel()
			l, r := base()
			tc.mutate(l, r)
			if trackerIssueEqual(l, r) {
				t.Fatalf("%s: expected not equal", tc.name)
			}
		})
	}
}

func TestGetConfig_StoreBackedBranch(t *testing.T) {
	ctx := context.Background()

	t.Run("non-yaml key reads from the store when set", func(t *testing.T) {
		tr := &Tracker{store: configStore{token: "  ds-from-store  "}}
		got := tr.getConfig(ctx, "notion.data_source_id", "NOTION_DATA_SOURCE_ID")
		if got != "ds-from-store" {
			t.Fatalf("getConfig = %q, want trimmed store value", got)
		}
	})

	t.Run("store error falls through to env var", func(t *testing.T) {
		t.Setenv("NOTION_DATA_SOURCE_ID", "env-ds")
		tr := &Tracker{store: configStore{err: context.DeadlineExceeded}}
		got := tr.getConfig(ctx, "notion.data_source_id", "NOTION_DATA_SOURCE_ID")
		if got != "env-ds" {
			t.Fatalf("getConfig on store error = %q, want env fallback", got)
		}
	})

	t.Run("empty store value falls through to env var", func(t *testing.T) {
		t.Setenv("NOTION_DATA_SOURCE_ID", "env-ds2")
		tr := &Tracker{store: configStore{token: "   "}}
		got := tr.getConfig(ctx, "notion.data_source_id", "NOTION_DATA_SOURCE_ID")
		if got != "env-ds2" {
			t.Fatalf("getConfig on empty store = %q, want env fallback", got)
		}
	})
}
