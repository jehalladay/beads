package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// mockStore implements just the storage.Storage methods the gitlab Tracker
// touches (GetConfig and GetDependenciesWithMetadata); all other methods
// come from the embedded nil interface and panic if called.
type mockStore struct {
	storage.Storage
	config map[string]string
	deps   map[string][]*types.IssueWithDependencyMetadata
}

func newMockStore() *mockStore {
	return &mockStore{
		config: map[string]string{},
		deps:   map[string][]*types.IssueWithDependencyMetadata{},
	}
}

func (m *mockStore) GetConfig(_ context.Context, key string) (string, error) {
	if v, ok := m.config[key]; ok {
		return v, nil
	}
	return "", nil
}

func (m *mockStore) GetDependenciesWithMetadata(_ context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	return m.deps[issueID], nil
}

func strptr(s string) *string { return &s }

// TestInitFromEnv covers Init's happy path plus the token- and project-missing
// guards, driven entirely through env vars (no live store rows needed).
func TestInitFromEnv(t *testing.T) {
	t.Run("missing token errors", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "")
		tr := &Tracker{}
		if err := tr.Init(context.Background(), newMockStore()); err == nil {
			t.Error("Init without token = nil error, want token error")
		}
	})

	t.Run("missing project errors", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "tok")
		t.Setenv("GITLAB_PROJECT_ID", "")
		t.Setenv("GITLAB_GROUP_ID", "")
		tr := &Tracker{}
		if err := tr.Init(context.Background(), newMockStore()); err == nil {
			t.Error("Init without project/group = nil error, want project error")
		}
	})

	t.Run("project mode succeeds", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "tok")
		t.Setenv("GITLAB_PROJECT_ID", "123")
		t.Setenv("GITLAB_GROUP_ID", "")
		t.Setenv("GITLAB_URL", "")
		t.Setenv("GITLAB_PROJECT_PATH", "grp/proj")
		tr := &Tracker{}
		if err := tr.Init(context.Background(), newMockStore()); err != nil {
			t.Fatalf("Init = %v, want nil", err)
		}
		if tr.client == nil {
			t.Fatal("Init left client nil")
		}
		if tr.client.BaseURL != "https://gitlab.com" {
			t.Errorf("BaseURL = %q, want the default gitlab.com", tr.client.BaseURL)
		}
		if tr.projectPath != "grp/proj" {
			t.Errorf("projectPath = %q, want grp/proj", tr.projectPath)
		}
	})

	t.Run("group mode falls back to default project id", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "tok")
		t.Setenv("GITLAB_PROJECT_ID", "")
		t.Setenv("GITLAB_GROUP_ID", "77")
		t.Setenv("GITLAB_DEFAULT_PROJECT_ID", "999")
		tr := &Tracker{}
		if err := tr.Init(context.Background(), newMockStore()); err != nil {
			t.Fatalf("Init(group) = %v, want nil", err)
		}
		if tr.client == nil {
			t.Fatal("Init(group) left client nil")
		}
	})
}

// TestLoadFilterConfig covers both the no-filter (nil) return and the populated
// filter, including the numeric project-id parse.
func TestLoadFilterConfig(t *testing.T) {
	t.Run("no config returns nil", func(t *testing.T) {
		t.Setenv("GITLAB_FILTER_LABELS", "")
		t.Setenv("GITLAB_FILTER_PROJECT", "")
		t.Setenv("GITLAB_FILTER_MILESTONE", "")
		t.Setenv("GITLAB_FILTER_ASSIGNEE", "")
		tr := &Tracker{store: newMockStore()}
		if f := tr.loadFilterConfig(context.Background()); f != nil {
			t.Errorf("loadFilterConfig with no config = %+v, want nil", f)
		}
	})

	t.Run("populated from env", func(t *testing.T) {
		t.Setenv("GITLAB_FILTER_LABELS", "bug,urgent")
		t.Setenv("GITLAB_FILTER_PROJECT", "42")
		t.Setenv("GITLAB_FILTER_MILESTONE", "v1")
		t.Setenv("GITLAB_FILTER_ASSIGNEE", "alice")
		tr := &Tracker{store: newMockStore()}
		f := tr.loadFilterConfig(context.Background())
		if f == nil {
			t.Fatal("loadFilterConfig = nil, want a filter")
		}
		if f.Labels != "bug,urgent" || f.Milestone != "v1" || f.Assignee != "alice" {
			t.Errorf("filter fields = %+v, want the env values", f)
		}
		if f.ProjectID != 42 {
			t.Errorf("ProjectID = %d, want 42", f.ProjectID)
		}
	})

	t.Run("non-numeric project id is ignored", func(t *testing.T) {
		t.Setenv("GITLAB_FILTER_LABELS", "bug")
		t.Setenv("GITLAB_FILTER_PROJECT", "not-a-number")
		t.Setenv("GITLAB_FILTER_MILESTONE", "")
		t.Setenv("GITLAB_FILTER_ASSIGNEE", "")
		tr := &Tracker{store: newMockStore()}
		f := tr.loadFilterConfig(context.Background())
		if f == nil {
			t.Fatal("loadFilterConfig = nil, want a filter")
		}
		if f.ProjectID != 0 {
			t.Errorf("ProjectID = %d, want 0 for non-numeric input", f.ProjectID)
		}
	})
}

// TestGetConfigStoreAndEnv covers getConfig's store-hit path and its env
// fallback for a non-yaml key.
func TestGetConfigStoreAndEnv(t *testing.T) {
	ctx := context.Background()

	t.Run("store value wins", func(t *testing.T) {
		st := newMockStore()
		st.config["gitlab.url"] = "https://gl.example.com"
		tr := &Tracker{store: st}
		got, err := tr.getConfig(ctx, "gitlab.url", "GITLAB_URL")
		if err != nil {
			t.Fatalf("getConfig error: %v", err)
		}
		if got != "https://gl.example.com" {
			t.Errorf("getConfig = %q, want the store value", got)
		}
	})

	t.Run("env fallback when store empty", func(t *testing.T) {
		t.Setenv("GITLAB_URL", "https://env.example.com")
		tr := &Tracker{store: newMockStore()}
		got, err := tr.getConfig(ctx, "gitlab.url", "GITLAB_URL")
		if err != nil {
			t.Fatalf("getConfig error: %v", err)
		}
		if got != "https://env.example.com" {
			t.Errorf("getConfig = %q, want the env value", got)
		}
	})
}

// TestFindParentEpicMilestone covers the walk-up-to-epic path (resolving a
// milestone IID to its API id) and the no-parent short-circuit.
func TestFindParentEpicMilestone(t *testing.T) {
	t.Run("resolves milestone from epic parent", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// FetchMilestoneByIID returns an array; first element is used.
			_ = json.NewEncoder(w).Encode([]Milestone{{ID: 55, IID: 5, Title: "Rel"}})
		}))
		defer server.Close()

		st := newMockStore()
		st.deps["child"] = []*types.IssueWithDependencyMetadata{
			{
				Issue: types.Issue{
					ID:          "epic-1",
					IssueType:   types.TypeEpic,
					ExternalRef: strptr("https://gitlab.com/g/p/-/milestones/5"),
				},
				DependencyType: types.DepParentChild,
			},
		}
		tr := &Tracker{client: NewClient("t", server.URL, "1"), store: st}
		if got := tr.findParentEpicMilestone(context.Background(), "child"); got != 55 {
			t.Errorf("findParentEpicMilestone = %d, want 55", got)
		}
	})

	t.Run("no parent returns zero", func(t *testing.T) {
		tr := &Tracker{store: newMockStore()}
		if got := tr.findParentEpicMilestone(context.Background(), "orphan"); got != 0 {
			t.Errorf("findParentEpicMilestone(orphan) = %d, want 0", got)
		}
	})

	t.Run("epic without milestone ref returns zero", func(t *testing.T) {
		st := newMockStore()
		st.deps["child"] = []*types.IssueWithDependencyMetadata{
			{
				Issue:          types.Issue{ID: "epic-1", IssueType: types.TypeEpic, ExternalRef: strptr("gitlab:9")},
				DependencyType: types.DepParentChild,
			},
		}
		tr := &Tracker{store: st}
		if got := tr.findParentEpicMilestone(context.Background(), "child"); got != 0 {
			t.Errorf("findParentEpicMilestone = %d, want 0 for non-milestone ref", got)
		}
	})
}

// TestFindParentStoryGID covers the story-parent resolution to a GraphQL GID
// and the no-parent case.
func TestFindParentStoryGID(t *testing.T) {
	t.Run("resolves gid from story parent", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"project":{"workItems":{"nodes":[{"id":"gid://gitlab/WorkItem/88"}]}}}}`))
		}))
		defer server.Close()

		st := newMockStore()
		st.deps["task-1"] = []*types.IssueWithDependencyMetadata{
			{
				Issue: types.Issue{
					ID:          "story-1",
					IssueType:   types.TypeStory,
					ExternalRef: strptr("https://gitlab.com/g/p/-/issues/42"),
				},
				DependencyType: types.DepParentChild,
			},
		}
		tr := &Tracker{client: NewClient("t", server.URL, "1"), store: st, projectPath: "g/p"}
		if got := tr.findParentStoryGID(context.Background(), "task-1"); got != "gid://gitlab/WorkItem/88" {
			t.Errorf("findParentStoryGID = %q, want the queried GID", got)
		}
	})

	t.Run("epic parent is skipped", func(t *testing.T) {
		st := newMockStore()
		st.deps["task-1"] = []*types.IssueWithDependencyMetadata{
			{
				Issue:          types.Issue{ID: "epic-1", IssueType: types.TypeEpic, ExternalRef: strptr("https://gitlab.com/g/p/-/milestones/5")},
				DependencyType: types.DepParentChild,
			},
		}
		tr := &Tracker{store: st, projectPath: "g/p"}
		if got := tr.findParentStoryGID(context.Background(), "task-1"); got != "" {
			t.Errorf("findParentStoryGID = %q, want empty (epic skipped)", got)
		}
	})

	t.Run("no deps returns empty", func(t *testing.T) {
		tr := &Tracker{store: newMockStore(), projectPath: "g/p"}
		if got := tr.findParentStoryGID(context.Background(), "nobody"); got != "" {
			t.Errorf("findParentStoryGID = %q, want empty", got)
		}
	})
}

// TestCreateIssueTaskWorkItem drives CreateIssue down the Task-with-story-parent
// branch, exercising createTaskWorkItem end to end against a GraphQL server.
func TestCreateIssueTaskWorkItem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		q := body.Query
		switch {
		case strings.Contains(q, "workItemCreate"):
			_, _ = w.Write([]byte(`{"data":{"workItemCreate":{"errors":[],"workItem":{"id":"gid://gitlab/WorkItem/1","iid":"3","title":"Task A","workItemType":{"name":"Task"},"webUrl":"u"}}}}`))
		case strings.Contains(q, "workItemTypes"):
			_, _ = w.Write([]byte(`{"data":{"project":{"workItemTypes":{"nodes":[{"id":"gid://gitlab/WorkItems::Type/5"}]}}}}`))
		case strings.Contains(q, "workItems"):
			// GetWorkItemGID lookup for the parent story.
			_, _ = w.Write([]byte(`{"data":{"project":{"workItems":{"nodes":[{"id":"gid://gitlab/WorkItem/456"}]}}}}`))
		default:
			t.Errorf("unexpected GraphQL query %q", q)
		}
	}))
	defer server.Close()

	st := newMockStore()
	st.deps["bd-1"] = []*types.IssueWithDependencyMetadata{
		{
			Issue: types.Issue{
				ID:          "story-1",
				IssueType:   types.TypeStory,
				ExternalRef: strptr("https://gitlab.com/g/p/-/issues/42"),
			},
			DependencyType: types.DepParentChild,
		},
	}
	tr := &Tracker{client: NewClient("t", server.URL, "1"), config: DefaultMappingConfig(), store: st, projectPath: "g/p"}

	issue := &types.Issue{ID: "bd-1", Title: "Task A", IssueType: types.TypeTask}
	ti, err := tr.CreateIssue(context.Background(), issue)
	if err != nil {
		t.Fatalf("CreateIssue(task-work-item) error: %v", err)
	}
	if ti == nil || ti.Identifier != "3" || ti.Title != "Task A" {
		t.Fatalf("got %+v, want task work item iid 3 titled Task A", ti)
	}
}
