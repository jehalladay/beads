package plane

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

const (
	stateBacklogID   = "57a70000-0000-0000-0000-000000000001"
	stateTodoID      = "57a70000-0000-0000-0000-000000000002"
	stateStartedID   = "57a70000-0000-0000-0000-000000000003"
	stateCompletedID = "57a70000-0000-0000-0000-000000000004"
	labelBackendID   = "1abe0000-0000-0000-0000-000000000001"
)

// trackerMux returns a mux serving the project, state, and label fixtures a
// Tracker needs for its caches.
func trackerMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"id": testProjectID, "name": "Gas City", "identifier": "GC",
			})
		})
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/states/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated([]any{
				map[string]any{"id": stateBacklogID, "name": "Backlog", "group": "backlog", "default": false},
				map[string]any{"id": stateTodoID, "name": "Todo", "group": "unstarted", "default": true},
				map[string]any{"id": stateStartedID, "name": "In Progress", "group": "started", "default": false},
				map[string]any{"id": stateCompletedID, "name": "Done", "group": "completed", "default": false},
			}, "", false))
		})
	return mux
}

// newInitializedTracker builds a Tracker wired to the test server, bypassing
// Init's config reads (tests are in-package, so unexported fields are fair
// game — the SetTeamIDs-style injection precedent from the linear adapter).
func newInitializedTracker(t *testing.T, mux *http.ServeMux) *Tracker {
	t.Helper()
	c, srv := newTestClient(t, mux)
	tr := &Tracker{
		client: c,
		refs: refContext{
			baseURL:   srv.URL,
			workspace: testWorkspace,
			projectID: testProjectID,
		},
	}
	return tr
}

func TestPlaneIsRegistered(t *testing.T) {
	factory := tracker.Get("plane")
	if factory == nil {
		t.Fatal("plane tracker not registered")
	}
	tr := factory()
	if tr.Name() != "plane" {
		t.Errorf("Name = %q", tr.Name())
	}
	if tr.DisplayName() != "Plane" {
		t.Errorf("DisplayName = %q", tr.DisplayName())
	}
	if tr.ConfigPrefix() != "plane" {
		t.Errorf("ConfigPrefix = %q", tr.ConfigPrefix())
	}
}

func TestInitRequiresConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "missing api key",
			env:     map[string]string{},
			wantErr: "PLANE_API_KEY",
		},
		{
			name:    "missing base url",
			env:     map[string]string{"PLANE_API_KEY": "k"},
			wantErr: "plane.base_url",
		},
		{
			name: "missing workspace",
			env: map[string]string{
				"PLANE_API_KEY": "k", "PLANE_BASE_URL": "https://p.example.com",
			},
			wantErr: "plane.workspace",
		},
		{
			name: "missing project",
			env: map[string]string{
				"PLANE_API_KEY": "k", "PLANE_BASE_URL": "https://p.example.com",
				"PLANE_WORKSPACE": "acme",
			},
			wantErr: "plane.project_id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range []string{"PLANE_API_KEY", "PLANE_BASE_URL", "PLANE_WORKSPACE", "PLANE_PROJECT_ID"} {
				t.Setenv(v, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			tr := &Tracker{}
			err := tr.Init(context.Background(), nil)
			if err == nil {
				t.Fatal("Init should fail")
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q missing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestInitFromEnv(t *testing.T) {
	t.Setenv("PLANE_API_KEY", "env-key")
	t.Setenv("PLANE_BASE_URL", "https://p.example.com/")
	t.Setenv("PLANE_WORKSPACE", "acme")
	t.Setenv("PLANE_PROJECT_ID", testProjectID)

	tr := &Tracker{}
	if err := tr.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if tr.client.BaseURL() != "https://p.example.com" {
		t.Errorf("base URL = %q", tr.client.BaseURL())
	}
	if err := tr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestValidateUninitialized(t *testing.T) {
	tr := &Tracker{}
	if err := tr.Validate(); err == nil {
		t.Error("Validate on uninitialized tracker should fail")
	}
}

func TestExternalRefMethods(t *testing.T) {
	tr := &Tracker{refs: refContext{
		baseURL: "https://p.example.com", workspace: testWorkspace, projectID: testProjectID,
	}}

	ref := "https://p.example.com/acme/projects/" + testProjectID + "/issues/" + testIssueID
	if !tr.IsExternalRef(ref) {
		t.Errorf("IsExternalRef(%q) = false", ref)
	}
	if tr.IsExternalRef("https://linear.app/team/issue/T-1") {
		t.Error("linear ref misidentified as plane")
	}
	if got := tr.ExtractIdentifier(ref); got != testIssueID {
		t.Errorf("ExtractIdentifier = %q, want issue UUID", got)
	}

	ti := &tracker.TrackerIssue{ID: testIssueID, URL: ref}
	if got := tr.BuildExternalRef(ti); got != ref {
		t.Errorf("BuildExternalRef = %q, want %q", got, ref)
	}
	// Without a URL the ref is constructed from instance coordinates.
	ti2 := &tracker.TrackerIssue{ID: testIssueID}
	if got := tr.BuildExternalRef(ti2); got != ref {
		t.Errorf("BuildExternalRef (no URL) = %q, want %q", got, ref)
	}
}

func TestFetchIssuesEnriches(t *testing.T) {
	mux := trackerMux(t)
	parentUUID := "deadbeef-0000-0000-0000-000000000001"
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/labels/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated([]any{
				map[string]any{"id": labelBackendID, "name": "backend"},
			}, "", false))
		})
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			issue := sampleIssueJSON(testIssueID, "enriched")
			issue["state"] = stateStartedID
			issue["labels"] = []string{labelBackendID}
			issue["parent"] = parentUUID
			writeJSON(w, http.StatusOK, paginated([]any{issue}, "", false))
		})
	tr := newInitializedTracker(t, mux)

	issues, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{})
	if err != nil {
		t.Fatalf("FetchIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues", len(issues))
	}
	ti := issues[0]

	if ti.Identifier != "GC-7" {
		t.Errorf("Identifier = %q, want GC-7 (project identifier + sequence)", ti.Identifier)
	}
	state, ok := ti.State.(*State)
	if !ok || state.Group != GroupStarted {
		t.Errorf("State = %#v, want *State with started group", ti.State)
	}
	if len(ti.Labels) != 1 || ti.Labels[0] != "backend" {
		t.Errorf("Labels = %v, want resolved names", ti.Labels)
	}
	if ti.ParentID != parentUUID {
		t.Errorf("ParentID = %q, want parent UUID", ti.ParentID)
	}
	if ti.URL == "" || !IsPlaneExternalRef(ti.URL) {
		t.Errorf("URL = %q, want a plane external ref", ti.URL)
	}
	if !contains(ti.Description, "desc") {
		t.Errorf("Description = %q, want markdown-converted content", ti.Description)
	}
	native, ok := ti.Raw.(*Issue)
	if !ok || native.ID != testIssueID {
		t.Errorf("Raw = %#v, want native *Issue", ti.Raw)
	}
}

func TestFetchIssuesSinceFilter(t *testing.T) {
	mux := trackerMux(t)
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/labels/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated(nil, "", false))
		})
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			old := sampleIssueJSON("00000000-0000-0000-0000-000000000001", "old")
			old["updated_at"] = "2026-01-01T00:00:00.000000Z"
			fresh := sampleIssueJSON("00000000-0000-0000-0000-000000000002", "fresh")
			fresh["updated_at"] = "2026-06-10T00:00:00.000000Z"
			writeJSON(w, http.StatusOK, paginated([]any{old, fresh}, "", false))
		})
	tr := newInitializedTracker(t, mux)

	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	issues, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{Since: &since})
	if err != nil {
		t.Fatalf("FetchIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Title != "fresh" {
		t.Errorf("issues = %+v, want only the fresh one", issues)
	}
}

func TestFetchIssueByUUIDAndIdentifier(t *testing.T) {
	mux := trackerMux(t)
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/labels/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated(nil, "", false))
		})
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/"+testIssueID+"/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "by uuid"))
		})
	mux.HandleFunc("/api/v1/workspaces/acme/work-items/GC-7/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "by human id"))
		})
	// Explicit 404 — ServeMux would otherwise prefix-match the project handler.
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/00000000-0000-0000-0000-00000000dead/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "The requested resource does not exist."})
		})
	tr := newInitializedTracker(t, mux)

	byUUID, err := tr.FetchIssue(context.Background(), testIssueID)
	if err != nil {
		t.Fatalf("FetchIssue(uuid): %v", err)
	}
	if byUUID == nil || byUUID.Title != "by uuid" {
		t.Errorf("byUUID = %+v", byUUID)
	}

	byHuman, err := tr.FetchIssue(context.Background(), "GC-7")
	if err != nil {
		t.Fatalf("FetchIssue(GC-7): %v", err)
	}
	if byHuman == nil || byHuman.Title != "by human id" {
		t.Errorf("byHuman = %+v", byHuman)
	}

	missing, err := tr.FetchIssue(context.Background(), "00000000-0000-0000-0000-00000000dead")
	if err != nil {
		t.Fatalf("FetchIssue(missing): %v", err)
	}
	if missing != nil {
		t.Errorf("missing = %+v, want nil", missing)
	}
}

func TestCreateIssueResolvesStateAndLabels(t *testing.T) {
	mux := trackerMux(t)
	var createdLabel, postedIssue map[string]any
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/labels/",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				_ = json.NewDecoder(r.Body).Decode(&createdLabel)
				writeJSON(w, http.StatusCreated, map[string]any{
					"id": "1abe0000-0000-0000-0000-00000000000e", "name": createdLabel["name"],
				})
				return
			}
			writeJSON(w, http.StatusOK, paginated([]any{
				map[string]any{"id": labelBackendID, "name": "backend"},
			}, "", false))
		})
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&postedIssue)
			resp := sampleIssueJSON(testIssueID, postedIssue["name"].(string))
			writeJSON(w, http.StatusCreated, resp)
		})
	tr := newInitializedTracker(t, mux)

	bead := &types.Issue{
		ID:          "bd-77",
		Title:       "epic work",
		Description: "# Plan\n\ndo it",
		Priority:    1,
		Status:      types.StatusInProgress,
		IssueType:   types.TypeEpic,
		Labels:      []string{"backend"},
	}
	created, err := tr.CreateIssue(context.Background(), bead)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created == nil {
		t.Fatal("created is nil")
	}

	if postedIssue["external_id"] != "bd-77" || postedIssue["external_source"] != ExternalSource {
		t.Errorf("external fields = %v/%v", postedIssue["external_id"], postedIssue["external_source"])
	}
	if postedIssue["state"] != stateStartedID {
		t.Errorf("state = %v, want started-group state %s", postedIssue["state"], stateStartedID)
	}
	if postedIssue["priority"] != "high" {
		t.Errorf("priority = %v", postedIssue["priority"])
	}
	html, _ := postedIssue["description_html"].(string)
	if !contains(html, "<h1>Plan</h1>") {
		t.Errorf("description_html = %q", html)
	}
	// Labels: existing "backend" reused, "beads:type:epic" created on demand.
	if createdLabel["name"] != "beads:type:epic" {
		t.Errorf("created label = %v, want beads:type:epic", createdLabel)
	}
	labels, _ := postedIssue["labels"].([]any)
	if len(labels) != 2 {
		t.Errorf("posted labels = %v, want existing + type label", labels)
	}
}

func TestCreateIssueDuplicateRecovers(t *testing.T) {
	mux := trackerMux(t)
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/labels/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated(nil, "", false))
		})
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Issue with the same external id and external source already exists",
				"id":    testIssueID,
			})
		})
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/"+testIssueID+"/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "pre-existing"))
		})
	tr := newInitializedTracker(t, mux)

	bead := &types.Issue{ID: "bd-42", Title: "dup", Status: types.StatusOpen}
	created, err := tr.CreateIssue(context.Background(), bead)
	if err != nil {
		t.Fatalf("CreateIssue should recover from 409, got: %v", err)
	}
	if created == nil || created.ID != testIssueID {
		t.Errorf("created = %+v, want the pre-existing issue", created)
	}
}

func TestUpdateIssuePushesMappedFields(t *testing.T) {
	mux := trackerMux(t)
	var patched map[string]any
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/labels/",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, paginated(nil, "", false))
		})
	mux.HandleFunc("/api/v1/workspaces/acme/projects/"+testProjectID+"/work-items/"+testIssueID+"/",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Errorf("method = %s", r.Method)
			}
			_ = json.NewDecoder(r.Body).Decode(&patched)
			writeJSON(w, http.StatusOK, sampleIssueJSON(testIssueID, "updated"))
		})
	tr := newInitializedTracker(t, mux)

	bead := &types.Issue{
		ID:     "bd-42",
		Title:  "updated title",
		Status: types.StatusClosed,
	}
	updated, err := tr.UpdateIssue(context.Background(), testIssueID, bead)
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated == nil {
		t.Fatal("updated is nil")
	}
	if patched["name"] != "updated title" {
		t.Errorf("name = %v", patched["name"])
	}
	if patched["state"] != stateCompletedID {
		t.Errorf("state = %v, want completed state %s", patched["state"], stateCompletedID)
	}
	if _, hasExt := patched["external_id"]; hasExt {
		t.Error("PATCH must not resend external_id (409 risk when it differs)")
	}
}
