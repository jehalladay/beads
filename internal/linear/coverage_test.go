package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// jsonServer returns an httptest.Server that responds to every request with the
// supplied GraphQL data payload (a single static page).
func jsonServer(t *testing.T, body map[string]interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- Client.FetchTeams ---

func TestFetchTeams(t *testing.T) {
	srv := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"teams": map[string]interface{}{
				"nodes": []interface{}{
					map[string]interface{}{"id": "team-1", "name": "Engineering", "key": "ENG"},
					map[string]interface{}{"id": "team-2", "name": "Design", "key": "DES"},
				},
			},
		},
	})

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	teams, err := client.FetchTeams(context.Background())
	if err != nil {
		t.Fatalf("FetchTeams: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("got %d teams, want 2", len(teams))
	}
	if teams[0].Key != "ENG" || teams[1].Name != "Design" {
		t.Errorf("unexpected teams: %+v", teams)
	}
}

func TestFetchTeams_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	if _, err := client.FetchTeams(context.Background()); err == nil {
		t.Fatal("expected error on server 500")
	}
}

// --- Client.FetchIssues ---

func TestFetchIssues_SinglePage(t *testing.T) {
	srv := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"issues": map[string]interface{}{
				"nodes": []interface{}{
					map[string]interface{}{"id": "i1", "identifier": "ENG-1", "title": "one"},
					map[string]interface{}{"id": "i2", "identifier": "ENG-2", "title": "two"},
				},
				"pageInfo": map[string]interface{}{"hasNextPage": false, "endCursor": ""},
			},
		},
	})

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	for _, state := range []string{"open", "closed", "all"} {
		issues, err := client.FetchIssues(context.Background(), state)
		if err != nil {
			t.Fatalf("FetchIssues(%q): %v", state, err)
		}
		if len(issues) != 2 {
			t.Fatalf("state %q: got %d issues, want 2", state, len(issues))
		}
	}
}

func TestFetchIssues_Pagination(t *testing.T) {
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes":    []interface{}{map[string]interface{}{"id": "i1", "identifier": "ENG-1"}},
						"pageInfo": map[string]interface{}{"hasNextPage": true, "endCursor": "cursor-1"},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes":    []interface{}{map[string]interface{}{"id": "i2", "identifier": "ENG-2"}},
					"pageInfo": map[string]interface{}{"hasNextPage": false, "endCursor": ""},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	client := NewClient("key", "team").WithProjectID("proj-1").WithEndpoint(srv.URL)
	issues, err := client.FetchIssues(context.Background(), "open")
	if err != nil {
		t.Fatalf("FetchIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues across pages, want 2", len(issues))
	}
	if call != 2 {
		t.Errorf("expected 2 HTTP calls for 2 pages, got %d", call)
	}
}

func TestFetchIssues_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	if _, err := client.FetchIssues(context.Background(), "open"); err == nil {
		t.Fatal("expected error on server 500")
	}
}

// --- Client.FetchProjects ---

func TestFetchProjects(t *testing.T) {
	srv := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"projects": map[string]interface{}{
				"nodes": []interface{}{
					map[string]interface{}{"id": "p1", "name": "Alpha", "state": "started"},
				},
				"pageInfo": map[string]interface{}{"hasNextPage": false, "endCursor": ""},
			},
		},
	})

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	for _, state := range []string{"started", "all", ""} {
		projects, err := client.FetchProjects(context.Background(), state)
		if err != nil {
			t.Fatalf("FetchProjects(%q): %v", state, err)
		}
		if len(projects) != 1 || projects[0].Name != "Alpha" {
			t.Fatalf("state %q: unexpected projects %+v", state, projects)
		}
	}
}

func TestFetchProjects_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	if _, err := client.FetchProjects(context.Background(), "all"); err == nil {
		t.Fatal("expected error on server 502")
	}
}

// --- Client.CreateProject / UpdateProject ---

func TestCreateProject(t *testing.T) {
	srv := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"projectCreate": map[string]interface{}{
				"success": true,
				"project": map[string]interface{}{"id": "p1", "name": "New", "state": "planned"},
			},
		},
	})

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	proj, err := client.CreateProject(context.Background(), "New", "desc", "planned")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.ID != "p1" || proj.Name != "New" {
		t.Errorf("unexpected project %+v", proj)
	}
}

func TestCreateProject_Unsuccessful(t *testing.T) {
	srv := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"projectCreate": map[string]interface{}{"success": false},
		},
	})

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	if _, err := client.CreateProject(context.Background(), "New", "", ""); err == nil {
		t.Fatal("expected error when projectCreate.success is false")
	}
}

func TestUpdateProject(t *testing.T) {
	srv := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"projectUpdate": map[string]interface{}{
				"success": true,
				"project": map[string]interface{}{"id": "p1", "name": "Renamed", "state": "started"},
			},
		},
	})

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	proj, err := client.UpdateProject(context.Background(), "p1", map[string]interface{}{"name": "Renamed"})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if proj.Name != "Renamed" {
		t.Errorf("unexpected project %+v", proj)
	}
}

func TestUpdateProject_Unsuccessful(t *testing.T) {
	srv := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"projectUpdate": map[string]interface{}{"success": false},
		},
	})

	client := NewClient("key", "team").WithEndpoint(srv.URL)
	if _, err := client.UpdateProject(context.Background(), "p1", nil); err == nil {
		t.Fatal("expected error when projectUpdate.success is false")
	}
}

// --- StateCache.FindStateForBeadsStatus ---

func TestFindStateForBeadsStatus(t *testing.T) {
	sc := &StateCache{States: []State{
		{ID: "s-open", Type: "unstarted"},
		{ID: "s-done", Type: "completed"},
	}}
	if got := sc.FindStateForBeadsStatus(types.StatusClosed); got != "s-done" {
		t.Errorf("closed -> %q, want s-done", got)
	}

	// No matching type falls back to the first state.
	scNoMatch := &StateCache{States: []State{{ID: "only", Type: "canceled"}}}
	if got := scNoMatch.FindStateForBeadsStatus(types.StatusInProgress); got != "only" {
		t.Errorf("fallback -> %q, want only", got)
	}

	// Empty cache returns "".
	empty := &StateCache{}
	if got := empty.FindStateForBeadsStatus(types.StatusOpen); got != "" {
		t.Errorf("empty cache -> %q, want empty string", got)
	}
}

// --- linearFieldMapper delegations (fieldmapper.go) ---

func TestFieldMapper_StatusToTracker(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}
	// Delegates to StatusToLinearStateType; closed -> completed.
	if got := m.StatusToTracker(types.StatusClosed); got != "completed" {
		t.Errorf("StatusToTracker(closed) = %v, want completed", got)
	}
}

func TestFieldMapper_TypeToBeads(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}
	// Non-*Labels input returns the TypeTask default.
	if got := m.TypeToBeads("not-labels"); got != types.TypeTask {
		t.Errorf("TypeToBeads(non-labels) = %v, want %v", got, types.TypeTask)
	}
	// *Labels input is routed through LabelToIssueType.
	if got := m.TypeToBeads(&Labels{Nodes: []Label{{Name: "unmatched"}}}); got == "" {
		t.Error("TypeToBeads(*Labels) returned empty type")
	}
}

func TestFieldMapper_TypeToTracker(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}
	if got := m.TypeToTracker(types.TypeBug); got != "bug" {
		t.Errorf("TypeToTracker(bug) = %v, want bug", got)
	}
}

func TestFieldMapper_IssueToBeads(t *testing.T) {
	m := &linearFieldMapper{config: DefaultMappingConfig()}

	// Non-*Issue raw returns nil.
	if got := m.IssueToBeads(&tracker.TrackerIssue{Raw: "nope"}); got != nil {
		t.Errorf("IssueToBeads(non-issue) = %v, want nil", got)
	}

	// A valid *Issue produces a conversion carrying the mapped issue.
	li := &Issue{
		ID:         "i1",
		Identifier: "ENG-1",
		Title:      "hello",
		State:      &State{Type: "unstarted"},
		CreatedAt:  "2026-01-01T00:00:00Z",
		UpdatedAt:  "2026-01-01T00:00:00Z",
	}
	conv := m.IssueToBeads(&tracker.TrackerIssue{Raw: li})
	if conv == nil || conv.Issue == nil {
		t.Fatalf("IssueToBeads(*Issue) = %v, want non-nil conversion", conv)
	}
	if conv.Issue.Title != "hello" {
		t.Errorf("converted title = %q, want hello", conv.Issue.Title)
	}
}

// --- mapping.go: ProjectToEpic / MapEpicToProjectState ---

func TestProjectToEpic(t *testing.T) {
	tests := []struct {
		state      string
		wantStatus types.Status
	}{
		{"completed", types.StatusClosed},
		{"canceled", types.StatusClosed},
		{"started", types.StatusInProgress},
		{"paused", types.StatusInProgress},
		{"planned", types.StatusOpen},
	}
	for _, tt := range tests {
		lp := &Project{
			Name:        "P",
			Description: "d",
			State:       tt.state,
			URL:         "https://linear.app/team/project/abc",
			CreatedAt:   "2026-01-01T00:00:00Z",
			UpdatedAt:   "2026-01-02T00:00:00Z",
		}
		epic := ProjectToEpic(lp)
		if epic.Status != tt.wantStatus {
			t.Errorf("state %q -> status %v, want %v", tt.state, epic.Status, tt.wantStatus)
		}
		if epic.IssueType != types.TypeEpic {
			t.Errorf("state %q -> type %v, want epic", tt.state, epic.IssueType)
		}
	}

	// Canceled sets a close reason.
	canceled := ProjectToEpic(&Project{State: "canceled", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"})
	if canceled.CloseReason != "canceled" {
		t.Errorf("canceled close reason = %q, want canceled", canceled.CloseReason)
	}

	// CompletedAt populates ClosedAt.
	done := ProjectToEpic(&Project{
		State:       "completed",
		CreatedAt:   "2026-01-01T00:00:00Z",
		UpdatedAt:   "2026-01-01T00:00:00Z",
		CompletedAt: "2026-01-03T00:00:00Z",
	})
	if done.ClosedAt == nil {
		t.Error("completed project should populate ClosedAt")
	}

	// Bad timestamps fall back to time.Now() without panicking.
	badTime := ProjectToEpic(&Project{State: "planned", CreatedAt: "not-a-time", UpdatedAt: "also-bad"})
	if badTime.CreatedAt.IsZero() || badTime.UpdatedAt.IsZero() {
		t.Error("bad timestamps should fall back to non-zero time.Now()")
	}
}

func TestMapEpicToProjectState(t *testing.T) {
	cases := map[types.Status]string{
		types.StatusClosed:     "completed",
		types.StatusInProgress: "started",
		types.StatusOpen:       "planned",
	}
	for status, want := range cases {
		if got := MapEpicToProjectState(status); got != want {
			t.Errorf("MapEpicToProjectState(%v) = %q, want %q", status, got, want)
		}
	}
}

// mockStore implements the config methods of storage.Storage for testing.
// All other methods panic if called (via the embedded nil interface).
type mockStore struct {
	storage.Storage
	config map[string]string
}

func newMockStore(config map[string]string) *mockStore {
	if config == nil {
		config = make(map[string]string)
	}
	return &mockStore{config: config}
}

func (m *mockStore) GetConfig(_ context.Context, key string) (string, error) {
	if v, ok := m.config[key]; ok {
		return v, nil
	}
	return "", nil
}

func (m *mockStore) GetAllConfig(_ context.Context) (map[string]string, error) {
	result := make(map[string]string, len(m.config))
	for k, v := range m.config {
		result[k] = v
	}
	return result, nil
}

func TestTracker_InitFromConfig(t *testing.T) {
	// linear.api_key is a yaml-only key resolved from the env; team_id/endpoint
	// come from the store.
	t.Setenv("LINEAR_API_KEY", "test-key")
	store := newMockStore(map[string]string{
		"linear.team_id":      "team-1",
		"linear.api_endpoint": "https://example.test/graphql",
		"linear.project_id":   "proj-1",
	})

	tr := &Tracker{}
	if err := tr.Init(context.Background(), store); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if tr.primaryClient() == nil {
		t.Fatal("Init did not create a client")
	}
	if tr.FieldMapper() == nil {
		t.Fatal("Init did not create a field mapper")
	}
	if tr.projectID != "proj-1" {
		t.Errorf("projectID = %q, want proj-1", tr.projectID)
	}
}

func TestTracker_InitMissingTeamID(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "test-key")
	t.Setenv("LINEAR_TEAM_ID", "")
	t.Setenv("LINEAR_TEAM_IDS", "")
	store := newMockStore(nil)

	tr := &Tracker{}
	if err := tr.Init(context.Background(), store); err == nil {
		t.Fatal("Init should fail when no team ID is configured")
	}
}

func TestTracker_GetAllConfigAdapter(t *testing.T) {
	store := newMockStore(map[string]string{"linear.state_map.todo": "open"})
	adapter := &configLoaderAdapter{ctx: context.Background(), store: store}
	cfg, err := adapter.GetAllConfig()
	if err != nil {
		t.Fatalf("GetAllConfig: %v", err)
	}
	if cfg["linear.state_map.todo"] != "open" {
		t.Errorf("unexpected config %+v", cfg)
	}
}

func TestBuildStateCacheFromTracker(t *testing.T) {
	srv := jsonServer(t, teamStatesResp("team-1", "s1", "Todo", "unstarted"))
	tr := trackerWithServer(srv.URL)

	cache, err := BuildStateCacheFromTracker(context.Background(), tr)
	if err != nil {
		t.Fatalf("BuildStateCacheFromTracker: %v", err)
	}
	if len(cache.States) != 1 || cache.States[0].ID != "s1" {
		t.Fatalf("unexpected cache %+v", cache)
	}

	// No client -> error.
	if _, err := BuildStateCacheFromTracker(context.Background(), &Tracker{}); err == nil {
		t.Fatal("expected error with no initialized client")
	}
}

func TestSyncLockHeldError(t *testing.T) {
	// nil Info -> generic message.
	e := &SyncLockHeldError{}
	if e.Error() == "" {
		t.Fatal("Error() returned empty string")
	}
	// With Info -> includes the PID.
	withInfo := &SyncLockHeldError{Info: &SyncLockInfo{PID: 4242, Started: time.Now()}}
	if !strings.Contains(withInfo.Error(), "4242") {
		t.Errorf("Error() = %q, want it to mention PID 4242", withInfo.Error())
	}
}

// --- Tracker adapter (tracker.go) ---

// trackerWithServer builds a single-team Tracker whose client points at srv,
// with an explicit state map so state resolution succeeds.
func trackerWithServer(url string) *Tracker {
	cfg := DefaultMappingConfig()
	cfg.ExplicitStateMap["todo"] = "open"
	cfg.ExplicitStateMap["in progress"] = "in_progress"
	cfg.ExplicitStateMap["done"] = "closed"
	return &Tracker{
		teamIDs: []string{"team-1"},
		clients: map[string]*Client{"team-1": NewClient("key", "team-1").WithEndpoint(url)},
		config:  cfg,
	}
}

// issuesPageBody is a one-page issues response containing the given nodes.
func issuesPageBody(nodes ...map[string]interface{}) map[string]interface{} {
	ns := make([]interface{}, len(nodes))
	for i, n := range nodes {
		ns[i] = n
	}
	return map[string]interface{}{
		"data": map[string]interface{}{
			"issues": map[string]interface{}{
				"nodes":    ns,
				"pageInfo": map[string]interface{}{"hasNextPage": false, "endCursor": ""},
			},
		},
	}
}

func TestTracker_FetchIssues(t *testing.T) {
	srv := jsonServer(t, issuesPageBody(
		map[string]interface{}{"id": "i1", "identifier": "ENG-1", "title": "one", "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"},
	))
	tr := trackerWithServer(srv.URL)

	issues, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{})
	if err != nil {
		t.Fatalf("FetchIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Identifier != "ENG-1" {
		t.Fatalf("unexpected issues %+v", issues)
	}
}

func TestTracker_FetchIssuesSince(t *testing.T) {
	srv := jsonServer(t, issuesPageBody(
		map[string]interface{}{"id": "i2", "identifier": "ENG-2", "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-05T00:00:00Z"},
	))
	tr := trackerWithServer(srv.URL)

	since := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	issues, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{Since: &since})
	if err != nil {
		t.Fatalf("FetchIssues(Since): %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
}

func TestTracker_FetchIssue(t *testing.T) {
	srv := jsonServer(t, issuesPageBody(
		map[string]interface{}{
			"id": "i9", "identifier": "ENG-9", "title": "found",
			"url":       "https://linear.app/team/issue/ENG-9",
			"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
		},
	))
	tr := trackerWithServer(srv.URL)

	ti, err := tr.FetchIssue(context.Background(), "ENG-9")
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if ti == nil || ti.Identifier != "ENG-9" {
		t.Fatalf("unexpected issue %+v", ti)
	}
}

func TestTracker_UpdateIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req GraphQLRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case containsQuery(req.Query, "TeamStates", "team("):
			_ = json.NewEncoder(w).Encode(teamStatesResp("team-1", "state-done", "Done", "completed"))
		case containsQuery(req.Query, "issueUpdate"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"issueUpdate": map[string]interface{}{
						"success": true,
						"issue": map[string]interface{}{
							"id": "i1", "identifier": "ENG-1", "title": "updated",
							"url":       "https://linear.app/team/issue/ENG-1",
							"createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-02T00:00:00Z",
						},
					},
				},
			})
		default:
			http.Error(w, "unexpected query", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	tr := trackerWithServer(srv.URL)
	updated, err := tr.UpdateIssue(context.Background(), "ENG-1", &types.Issue{Title: "updated", Status: types.StatusClosed})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.Title != "updated" {
		t.Errorf("unexpected updated issue %+v", updated)
	}
}

func TestTracker_MappingConfigAndClose(t *testing.T) {
	tr := trackerWithServer("http://unused")
	if tr.MappingConfig() == nil {
		t.Error("MappingConfig() returned nil")
	}
	if err := tr.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestTracker_ValidatePushStateMappings(t *testing.T) {
	// No explicit map -> error.
	empty := &Tracker{teamIDs: []string{"team-1"}, config: DefaultMappingConfig()}
	if err := empty.ValidatePushStateMappings(context.Background()); err == nil {
		t.Fatal("expected error when ExplicitStateMap is empty")
	}

	// With a full explicit map and a server that returns matching states, it passes.
	srv := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"team": map[string]interface{}{
				"id": "team-1",
				"states": map[string]interface{}{
					"nodes": []interface{}{
						map[string]interface{}{"id": "s1", "name": "Todo", "type": "unstarted"},
						map[string]interface{}{"id": "s2", "name": "In Progress", "type": "started"},
						map[string]interface{}{"id": "s3", "name": "Done", "type": "completed"},
					},
				},
			},
		},
	})
	tr := trackerWithServer(srv.URL)
	tr.config.ExplicitStateMap["todo"] = "open"
	tr.config.ExplicitStateMap["in progress"] = "in_progress"
	tr.config.ExplicitStateMap["done"] = "closed"
	if err := tr.ValidatePushStateMappings(context.Background()); err != nil {
		t.Fatalf("ValidatePushStateMappings: %v", err)
	}
}

// containsQuery reports whether q contains any of the given substrings.
func containsQuery(q string, subs ...string) bool {
	for _, s := range subs {
		if s != "" && strings.Contains(q, s) {
			return true
		}
	}
	return false
}

// --- types.go: error stringers ---

func TestErrRateLimitExhausted_Error(t *testing.T) {
	e := &ErrRateLimitExhausted{Remaining: 3, Floor: 10}
	msg := e.Error()
	if msg == "" {
		t.Fatal("Error() returned empty string")
	}
	if !e.RateLimitExhausted() {
		t.Error("RateLimitExhausted() should be true")
	}

	// With a reset time, the message includes the reset clause.
	resetsAt, err := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parse reset time: %v", err)
	}
	withReset := &ErrRateLimitExhausted{Remaining: 1, Floor: 5, ResetsAt: resetsAt}
	if got := withReset.Error(); got == msg {
		t.Error("Error() with ResetsAt should differ from the base message")
	}
}
