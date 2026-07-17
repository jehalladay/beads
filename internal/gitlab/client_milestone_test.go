package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchMilestones verifies the REST list call parses an array response and
// forwards the state filter.
func TestFetchMilestones(t *testing.T) {
	t.Parallel()
	var gotState string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotState = r.URL.Query().Get("state")
		if !strings.Contains(r.URL.Path, "/milestones") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]Milestone{
			{ID: 1, IID: 10, Title: "v1.0", State: "active"},
			{ID: 2, IID: 11, Title: "v2.0", State: "active"},
		})
	}))
	defer server.Close()

	client := NewClient("token", server.URL, "123")
	milestones, err := client.FetchMilestones(context.Background(), "active")
	if err != nil {
		t.Fatalf("FetchMilestones error: %v", err)
	}
	if len(milestones) != 2 {
		t.Fatalf("got %d milestones, want 2", len(milestones))
	}
	if gotState != "active" {
		t.Errorf("state param = %q, want \"active\"", gotState)
	}
	if milestones[0].Title != "v1.0" {
		t.Errorf("milestone[0].Title = %q, want \"v1.0\"", milestones[0].Title)
	}
}

// TestFetchMilestoneByIID covers both the found and not-found (empty array)
// cases.
func TestFetchMilestoneByIID(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("iids[]"); got != "10" {
				t.Errorf("iids[] = %q, want \"10\"", got)
			}
			_ = json.NewEncoder(w).Encode([]Milestone{{ID: 1, IID: 10, Title: "v1.0"}})
		}))
		defer server.Close()

		client := NewClient("token", server.URL, "123")
		ms, err := client.FetchMilestoneByIID(context.Background(), 10)
		if err != nil {
			t.Fatalf("FetchMilestoneByIID error: %v", err)
		}
		if ms == nil || ms.IID != 10 {
			t.Fatalf("got %+v, want milestone IID 10", ms)
		}
	})

	t.Run("not found returns nil", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]Milestone{})
		}))
		defer server.Close()

		client := NewClient("token", server.URL, "123")
		ms, err := client.FetchMilestoneByIID(context.Background(), 99)
		if err != nil {
			t.Fatalf("FetchMilestoneByIID error: %v", err)
		}
		if ms != nil {
			t.Errorf("got %+v, want nil for missing milestone", ms)
		}
	})
}

// TestCreateMilestone verifies the POST body and parsed response.
func TestCreateMilestone(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		_ = json.Unmarshal(body, &payload)
		if payload["title"] != "Sprint 1" {
			t.Errorf("body title = %v, want \"Sprint 1\"", payload["title"])
		}
		_ = json.NewEncoder(w).Encode(Milestone{ID: 5, IID: 3, Title: "Sprint 1", Description: "desc"})
	}))
	defer server.Close()

	client := NewClient("token", server.URL, "123")
	ms, err := client.CreateMilestone(context.Background(), "Sprint 1", "desc")
	if err != nil {
		t.Fatalf("CreateMilestone error: %v", err)
	}
	if ms.Title != "Sprint 1" || ms.ID != 5 {
		t.Errorf("got %+v, want ID 5 title \"Sprint 1\"", ms)
	}
}

// TestUpdateMilestone verifies the PUT path, updates body, and response parse.
func TestUpdateMilestone(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %q, want PUT", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/milestones/5") {
			t.Errorf("path = %q, want to end with /milestones/5", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Milestone{ID: 5, IID: 3, Title: "Renamed", State: "closed"})
	}))
	defer server.Close()

	client := NewClient("token", server.URL, "123")
	ms, err := client.UpdateMilestone(context.Background(), 5, map[string]interface{}{"state_event": "close"})
	if err != nil {
		t.Fatalf("UpdateMilestone error: %v", err)
	}
	if ms.State != "closed" || ms.Title != "Renamed" {
		t.Errorf("got %+v, want state closed title Renamed", ms)
	}
}

// TestListProjects verifies the membership list call and array parsing.
func TestListProjects(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("membership"); got != "true" {
			t.Errorf("membership = %q, want \"true\"", got)
		}
		_ = json.NewEncoder(w).Encode([]Project{
			{ID: 1, Name: "alpha", PathWithNamespace: "grp/alpha"},
			{ID: 2, Name: "beta", PathWithNamespace: "grp/beta"},
		})
	}))
	defer server.Close()

	client := NewClient("token", server.URL, "123")
	projects, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects error: %v", err)
	}
	if len(projects) != 2 || projects[1].Name != "beta" {
		t.Fatalf("got %+v, want 2 projects incl. beta", projects)
	}
}
