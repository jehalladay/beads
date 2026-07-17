package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// newTestTracker builds a Tracker wired to an httptest server, bypassing Init
// (which needs storage/config). store is left nil so parent-lookup branches
// are skipped.
func newTestTracker(serverURL string) *Tracker {
	return &Tracker{
		client: NewClient("token", serverURL, "123"),
		config: DefaultMappingConfig(),
	}
}

// TestTrackerMetadata covers the trivial metadata accessors and lifecycle
// no-ops.
func TestTrackerMetadata(t *testing.T) {
	t.Parallel()
	tr := &Tracker{}
	if tr.Name() != "gitlab" {
		t.Errorf("Name() = %q, want gitlab", tr.Name())
	}
	if tr.DisplayName() != "GitLab" {
		t.Errorf("DisplayName() = %q, want GitLab", tr.DisplayName())
	}
	if tr.ConfigPrefix() != "gitlab" {
		t.Errorf("ConfigPrefix() = %q, want gitlab", tr.ConfigPrefix())
	}
	if err := tr.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// TestTrackerValidate covers both the uninitialized and initialized branches.
func TestTrackerValidate(t *testing.T) {
	t.Parallel()
	if err := (&Tracker{}).Validate(); err == nil {
		t.Error("Validate() on uninitialized tracker = nil, want error")
	}
	tr := newTestTracker("https://example.com")
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate() on initialized tracker = %v, want nil", err)
	}
}

// TestTrackerSetFilterAndFieldMapper covers SetFilter and FieldMapper wiring.
func TestTrackerSetFilterAndFieldMapper(t *testing.T) {
	t.Parallel()
	tr := newTestTracker("https://example.com")
	f := &IssueFilter{Labels: "bug"}
	tr.SetFilter(f)
	if tr.filter != f {
		t.Error("SetFilter did not store the filter")
	}
	fm := tr.FieldMapper()
	if fm == nil {
		t.Fatal("FieldMapper() = nil")
	}
	if got := fm.StatusToBeads("closed"); got != types.StatusClosed {
		t.Errorf("FieldMapper StatusToBeads(closed) = %q, want closed", got)
	}
}

// TestTrackerIsMilestoneRef covers the milestone-vs-issue ref discriminator.
func TestTrackerIsMilestoneRef(t *testing.T) {
	t.Parallel()
	tr := &Tracker{}
	cases := []struct {
		ref  string
		want bool
	}{
		{"https://gitlab.com/g/p/-/milestones/5", true},
		{"https://gitlab.com/g/p/-/issues/42", false},
		{"gitlab:42", false},
	}
	for _, c := range cases {
		if got := tr.IsMilestoneRef(c.ref); got != c.want {
			t.Errorf("IsMilestoneRef(%q) = %v, want %v", c.ref, got, c.want)
		}
	}
}

// TestTrackerFetchIssues covers the list+enrich path (issues plus their links).
func TestTrackerFetchIssues(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/links"):
			_ = json.NewEncoder(w).Encode([]IssueLink{})
		case strings.HasSuffix(r.URL.Path, "/issues"):
			_ = json.NewEncoder(w).Encode([]Issue{
				{ID: 1, IID: 10, Title: "First", State: "opened"},
				{ID: 2, IID: 11, Title: "Second", State: "closed"},
			})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	tr := newTestTracker(server.URL)
	issues, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{State: "open"})
	if err != nil {
		t.Fatalf("FetchIssues error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if issues[0].Title != "First" {
		t.Errorf("issues[0].Title = %q, want First", issues[0].Title)
	}
}

// TestTrackerFetchIssue covers the single-issue path plus the invalid-IID guard.
func TestTrackerFetchIssue(t *testing.T) {
	t.Parallel()

	t.Run("invalid iid", func(t *testing.T) {
		t.Parallel()
		tr := newTestTracker("https://example.com")
		if _, err := tr.FetchIssue(context.Background(), "not-a-number"); err == nil {
			t.Error("FetchIssue(non-numeric) = nil error, want parse error")
		}
	})

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(Issue{ID: 1, IID: 42, Title: "Found", State: "opened"})
		}))
		defer server.Close()

		tr := newTestTracker(server.URL)
		ti, err := tr.FetchIssue(context.Background(), "42")
		if err != nil {
			t.Fatalf("FetchIssue error: %v", err)
		}
		if ti == nil || ti.Title != "Found" {
			t.Fatalf("got %+v, want issue titled Found", ti)
		}
	})
}

// TestTrackerCreateIssue covers creating a plain (non-epic, no-parent) issue.
func TestTrackerCreateIssue(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		_ = json.NewEncoder(w).Encode(Issue{ID: 9, IID: 3, Title: "New issue", State: "opened"})
	}))
	defer server.Close()

	tr := newTestTracker(server.URL)
	issue := &types.Issue{ID: "bd-1", Title: "New issue", IssueType: types.TypeTask}
	ti, err := tr.CreateIssue(context.Background(), issue)
	if err != nil {
		t.Fatalf("CreateIssue error: %v", err)
	}
	if ti == nil || ti.Identifier != "3" {
		t.Fatalf("got %+v, want tracker issue with identifier 3", ti)
	}
}

// TestTrackerCreateEpicMilestone covers the epic→milestone branch of CreateIssue.
func TestTrackerCreateEpicMilestone(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/milestones") {
			t.Errorf("path = %q, want a /milestones call", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Milestone{ID: 7, IID: 2, Title: "Epic ms", WebURL: "http://x/-/milestones/7"})
	}))
	defer server.Close()

	tr := newTestTracker(server.URL)
	issue := &types.Issue{ID: "bd-e", Title: "Epic ms", IssueType: types.TypeEpic}
	ti, err := tr.CreateIssue(context.Background(), issue)
	if err != nil {
		t.Fatalf("CreateIssue(epic) error: %v", err)
	}
	if ti == nil || ti.ID != "7" {
		t.Fatalf("got %+v, want milestone-backed tracker issue id 7", ti)
	}
}

// TestTrackerUpdateIssue covers the plain update path and the invalid-IID guard.
func TestTrackerUpdateIssue(t *testing.T) {
	t.Parallel()

	t.Run("invalid external id", func(t *testing.T) {
		t.Parallel()
		tr := newTestTracker("https://example.com")
		issue := &types.Issue{ID: "bd-1", Title: "x", IssueType: types.TypeTask}
		if _, err := tr.UpdateIssue(context.Background(), "nan", issue); err == nil {
			t.Error("UpdateIssue(non-numeric) = nil error, want parse error")
		}
	})

	t.Run("updates issue", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(Issue{ID: 1, IID: 42, Title: "Updated", State: "opened"})
		}))
		defer server.Close()

		tr := newTestTracker(server.URL)
		issue := &types.Issue{ID: "bd-1", Title: "Updated", IssueType: types.TypeTask}
		ti, err := tr.UpdateIssue(context.Background(), "42", issue)
		if err != nil {
			t.Fatalf("UpdateIssue error: %v", err)
		}
		if ti == nil || ti.Title != "Updated" {
			t.Fatalf("got %+v, want issue titled Updated", ti)
		}
	})
}

// TestTrackerUpdateEpicMilestone covers the epic→updateMilestone branch,
// including the closed state_event.
func TestTrackerUpdateEpicMilestone(t *testing.T) {
	t.Parallel()

	t.Run("invalid milestone id", func(t *testing.T) {
		t.Parallel()
		tr := newTestTracker("https://example.com")
		issue := &types.Issue{ID: "bd-e", Title: "e", IssueType: types.TypeEpic}
		if _, err := tr.UpdateIssue(context.Background(), "nan", issue); err == nil {
			t.Error("UpdateIssue(epic, non-numeric) = nil error, want parse error")
		}
	})

	t.Run("closes milestone", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method = %q, want PUT", r.Method)
			}
			_ = json.NewEncoder(w).Encode(Milestone{ID: 7, IID: 2, Title: "Done", State: "closed", WebURL: "http://x/-/milestones/7"})
		}))
		defer server.Close()

		tr := newTestTracker(server.URL)
		issue := &types.Issue{ID: "bd-e", Title: "Done", IssueType: types.TypeEpic, Status: types.StatusClosed}
		ti, err := tr.UpdateIssue(context.Background(), "7", issue)
		if err != nil {
			t.Fatalf("UpdateIssue(epic) error: %v", err)
		}
		if ti == nil || ti.ID != "7" {
			t.Fatalf("got %+v, want milestone tracker issue id 7", ti)
		}
	})
}

// TestMilestoneToTrackerIssue covers the pure milestone→TrackerIssue converter.
func TestMilestoneToTrackerIssue(t *testing.T) {
	t.Parallel()
	ms := &Milestone{ID: 12, IID: 4, Title: "Release", WebURL: "http://x/-/milestones/12"}
	ti := milestoneToTrackerIssue(ms)
	if ti.ID != "12" || ti.Identifier != "12" {
		t.Errorf("ID/Identifier = %q/%q, want 12/12", ti.ID, ti.Identifier)
	}
	if ti.Title != "Release" || ti.URL != "http://x/-/milestones/12" {
		t.Errorf("Title/URL = %q/%q, want Release/the web url", ti.Title, ti.URL)
	}
}
