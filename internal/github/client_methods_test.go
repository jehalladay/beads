package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// newMethodTestClient builds a client pointed at a test server via WithBaseURL,
// so no real network is touched. Retries are irrelevant for these 2xx paths.
func newMethodTestClient(baseURL string) *Client {
	return NewClient("test-token", "owner", "repo").WithBaseURL(baseURL + "/")
}

func writeJSON(t *testing.T, w http.ResponseWriter, v interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestWithBaseURLAndWithHTTPClient(t *testing.T) {
	base := NewClient("tok", "o", "r")

	got := base.WithBaseURL("https://ghe.example.com/api/v3/")
	if got.BaseURL != "https://ghe.example.com/api/v3" {
		t.Errorf("WithBaseURL did not trim trailing slash: %q", got.BaseURL)
	}
	if got.Token != "tok" || got.Owner != "o" || got.Repo != "r" {
		t.Errorf("WithBaseURL dropped identity fields: %+v", got)
	}

	custom := &http.Client{}
	hc := base.WithHTTPClient(custom)
	if hc.HTTPClient != custom {
		t.Error("WithHTTPClient did not set the provided client")
	}
	if hc.BaseURL != base.BaseURL || hc.Token != base.Token {
		t.Error("WithHTTPClient dropped config fields")
	}
}

func TestRepoPath(t *testing.T) {
	c := NewClient("tok", "octocat", "hello-world")
	if got := c.repoPath(); got != "/repos/octocat/hello-world" {
		t.Errorf("repoPath() = %q", got)
	}
}

func TestFetchIssues_FiltersPRsAndPaginates(t *testing.T) {
	var page1URL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			// Link header points to page 2 to exercise nextPageURL + loop.
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, page1URL))
			writeJSON(t, w, []Issue{
				{Number: 1, Title: "real"},
				{Number: 2, Title: "a PR", PullRequest: &PullRequestRef{URL: "x"}},
			})
		default:
			writeJSON(t, w, []Issue{{Number: 3, Title: "second page"}})
		}
	}))
	defer srv.Close()
	page1URL = srv.URL

	c := newMethodTestClient(srv.URL)
	issues, err := c.FetchIssues(context.Background(), "all")
	if err != nil {
		t.Fatalf("FetchIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 non-PR issues across 2 pages, got %d: %+v", len(issues), issues)
	}
	if issues[0].Number != 1 || issues[1].Number != 3 {
		t.Errorf("unexpected issue numbers: %d, %d", issues[0].Number, issues[1].Number)
	}
}

func TestFetchIssuesSince_SendsSinceParam(t *testing.T) {
	var gotSince string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSince = r.URL.Query().Get("since")
		writeJSON(t, w, []Issue{{Number: 7, Title: "since"}})
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	since := mustTime(t, "2026-01-02T03:04:05Z")
	issues, err := c.FetchIssuesSince(context.Background(), "open", since)
	if err != nil {
		t.Fatalf("FetchIssuesSince: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 7 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if gotSince != "2026-01-02T03:04:05Z" {
		t.Errorf("since param = %q, want RFC3339 UTC", gotSince)
	}
}

func TestCreateIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["title"] != "new" {
			t.Errorf("title = %v", body["title"])
		}
		if _, ok := body["labels"]; !ok {
			t.Error("labels not forwarded")
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, Issue{Number: 42, Title: "new"})
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	issue, err := c.CreateIssue(context.Background(), "new", "body", []string{"bug"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.Number != 42 {
		t.Errorf("issue.Number = %d, want 42", issue.Number)
	}
}

func TestUpdateIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/issues/5") {
			t.Errorf("path = %q, want .../issues/5", r.URL.Path)
		}
		writeJSON(t, w, Issue{Number: 5, State: "closed"})
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	issue, err := c.UpdateIssue(context.Background(), 5, map[string]interface{}{"state": "closed"})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if issue.State != "closed" {
		t.Errorf("state = %q, want closed", issue.State)
	}
}

func TestFetchIssueByNumber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues/9") {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(t, w, Issue{Number: 9, Title: "one"})
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	issue, err := c.FetchIssueByNumber(context.Background(), 9)
	if err != nil {
		t.Fatalf("FetchIssueByNumber: %v", err)
	}
	if issue.Number != 9 {
		t.Errorf("number = %d, want 9", issue.Number)
	}
}

func TestListRepositories(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/user/repos") {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(t, w, []Repository{{Name: "a"}, {Name: "b"}})
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	repos, err := c.ListRepositories(context.Background())
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
}

func TestAddLabelsAndRemoveLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if !strings.HasSuffix(r.URL.Path, "/issues/3/labels") {
				t.Errorf("add path = %q", r.URL.Path)
			}
		case http.MethodDelete:
			if !strings.HasSuffix(r.URL.Path, "/issues/3/labels/wontfix") {
				t.Errorf("remove path = %q", r.URL.Path)
			}
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
		writeJSON(t, w, []Label{})
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	if err := c.AddLabels(context.Background(), 3, []string{"bug"}); err != nil {
		t.Fatalf("AddLabels: %v", err)
	}
	if err := c.RemoveLabel(context.Background(), 3, "wontfix"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
}

func TestGetAuthenticatedUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/user") {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(t, w, User{Login: "octocat", ID: 1})
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	user, err := c.GetAuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("GetAuthenticatedUser: %v", err)
	}
	if user.Login != "octocat" {
		t.Errorf("login = %q, want octocat", user.Login)
	}
}

func TestClientMethods_ErrorPropagation(t *testing.T) {
	// A 404 (non-retryable, non-rate-limit) surfaces as a plain API error from
	// each wrapper. This exercises the error-return arms of the wrappers.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	c := newMethodTestClient(srv.URL)
	ctx := context.Background()

	if _, err := c.FetchIssues(ctx, "all"); err == nil {
		t.Error("FetchIssues: expected error")
	}
	if _, err := c.FetchIssuesSince(ctx, "all", mustTime(t, "2026-01-01T00:00:00Z")); err == nil {
		t.Error("FetchIssuesSince: expected error")
	}
	if _, err := c.CreateIssue(ctx, "t", "b", nil); err == nil {
		t.Error("CreateIssue: expected error")
	}
	if _, err := c.UpdateIssue(ctx, 1, nil); err == nil {
		t.Error("UpdateIssue: expected error")
	}
	if _, err := c.FetchIssueByNumber(ctx, 1); err == nil {
		t.Error("FetchIssueByNumber: expected error")
	}
	if _, err := c.ListRepositories(ctx); err == nil {
		t.Error("ListRepositories: expected error")
	}
	if err := c.AddLabels(ctx, 1, []string{"x"}); err == nil {
		t.Error("AddLabels: expected error")
	}
	if err := c.RemoveLabel(ctx, 1, "x"); err == nil {
		t.Error("RemoveLabel: expected error")
	}
	if _, err := c.GetAuthenticatedUser(ctx); err == nil {
		t.Error("GetAuthenticatedUser: expected error")
	}
}

func TestNextPageURL(t *testing.T) {
	tests := []struct {
		name string
		link string
		want string
	}{
		{"empty", "", ""},
		{"no next rel", `<https://api.github.com/x?page=2>; rel="prev"`, ""},
		{"has next", `<https://api.github.com/x?page=2>; rel="next"`, "https://api.github.com/x?page=2"},
		{"next among several", `<https://a/1>; rel="prev", <https://a/3>; rel="next"`, "https://a/3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.link != "" {
				h.Set("Link", tt.link)
			}
			if got := nextPageURL(h); got != tt.want {
				t.Errorf("nextPageURL(%q) = %q, want %q", tt.link, got, tt.want)
			}
		})
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tv, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tv
}

// ---- pure field-mapper inverse lookups (0% before) ----

func TestFieldMapper_TrackerConversions(t *testing.T) {
	m := &githubFieldMapper{config: DefaultMappingConfig()}

	// PriorityToTracker: inverse lookup returns a label whose PriorityMap value
	// is the given beads priority; unknown priority -> "medium".
	label := m.PriorityToTracker(0)
	if s, ok := label.(string); !ok || m.config.PriorityMap[s] != 0 {
		t.Errorf("PriorityToTracker(0) = %v, not a label mapping to 0", label)
	}
	if got := m.PriorityToTracker(-999); got != "medium" {
		t.Errorf("PriorityToTracker(unknown) = %v, want medium", got)
	}

	// StatusToTracker: closed -> "closed", everything else -> "open".
	if got := m.StatusToTracker(types.StatusClosed); got != "closed" {
		t.Errorf("StatusToTracker(closed) = %v", got)
	}
	if got := m.StatusToTracker(types.StatusOpen); got != "open" {
		t.Errorf("StatusToTracker(open) = %v", got)
	}
	if got := m.StatusToTracker(types.StatusInProgress); got != "open" {
		t.Errorf("StatusToTracker(in_progress) = %v, want open", got)
	}

	// TypeToTracker: passthrough of the beads type string.
	if got := m.TypeToTracker(types.TypeBug); got != string(types.TypeBug) {
		t.Errorf("TypeToTracker(bug) = %v, want %q", got, string(types.TypeBug))
	}
}
