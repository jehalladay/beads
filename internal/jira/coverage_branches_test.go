package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// errServer returns an httptest server that always responds with the given
// permanent status code and body, exercising the client error branches.
func errServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// jsonServer serves the given raw body verbatim with a 200, exercising decode paths.
func jsonServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchIssueTimestampErrorBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("http error", func(t *testing.T) {
		c := newTestClient(errServer(t, http.StatusNotFound, "nope").URL, "3")
		if _, err := c.FetchIssueTimestamp(ctx, "PROJ-1"); err == nil {
			t.Fatal("expected error on 404")
		}
	})

	t.Run("bad json", func(t *testing.T) {
		c := newTestClient(jsonServer(t, "{not json").URL, "3")
		if _, err := c.FetchIssueTimestamp(ctx, "PROJ-1"); err == nil {
			t.Fatal("expected parse error")
		}
	})

	t.Run("bad timestamp", func(t *testing.T) {
		c := newTestClient(jsonServer(t, `{"fields":{"updated":"not-a-time"}}`).URL, "3")
		if _, err := c.FetchIssueTimestamp(ctx, "PROJ-1"); err == nil {
			t.Fatal("expected timestamp parse error")
		}
	})

	t.Run("success", func(t *testing.T) {
		c := newTestClient(jsonServer(t, `{"fields":{"updated":"2026-01-02T03:04:05.000+0000"}}`).URL, "3")
		got, err := c.FetchIssueTimestamp(ctx, "PROJ-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.IsZero() {
			t.Fatal("expected non-zero timestamp")
		}
	})
}

func TestCreateIssueErrorBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("post error", func(t *testing.T) {
		c := newTestClient(errServer(t, http.StatusBadRequest, "bad").URL, "3")
		if _, err := c.CreateIssue(ctx, map[string]interface{}{"summary": "x"}); err == nil {
			t.Fatal("expected create error")
		}
	})

	t.Run("bad create response json", func(t *testing.T) {
		c := newTestClient(jsonServer(t, "{bad").URL, "3")
		if _, err := c.CreateIssue(ctx, map[string]interface{}{"summary": "x"}); err == nil {
			t.Fatal("expected parse error on create response")
		}
	})
}

func TestUpdateIssueClientErrorBranch(t *testing.T) {
	c := newTestClient(errServer(t, http.StatusBadRequest, "bad").URL, "3")
	if err := c.UpdateIssue(context.Background(), "PROJ-1", map[string]interface{}{"summary": "x"}); err == nil {
		t.Fatal("expected update error")
	}
}

func TestGetIssueTransitionsErrorBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("http error", func(t *testing.T) {
		c := newTestClient(errServer(t, http.StatusNotFound, "nope").URL, "3")
		if _, err := c.GetIssueTransitions(ctx, "PROJ-1"); err == nil {
			t.Fatal("expected transitions error")
		}
	})

	t.Run("bad json", func(t *testing.T) {
		c := newTestClient(jsonServer(t, "{bad").URL, "3")
		if _, err := c.GetIssueTransitions(ctx, "PROJ-1"); err == nil {
			t.Fatal("expected parse error")
		}
	})
}

func TestTransitionIssueClientErrorBranch(t *testing.T) {
	c := newTestClient(errServer(t, http.StatusBadRequest, "bad").URL, "3")
	if err := c.TransitionIssue(context.Background(), "PROJ-1", "31"); err == nil {
		t.Fatal("expected transition error")
	}
}

func TestGetIssueBadJSONBranch(t *testing.T) {
	c := newTestClient(jsonServer(t, "{not-an-issue").URL, "3")
	if _, err := c.GetIssue(context.Background(), "PROJ-1"); err == nil {
		t.Fatal("expected parse error on get issue")
	}
}

func TestDescriptionToPlainTextEdgeBranches(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		// Non-"doc" JSON object that decodes but isn't ADF; falls back and
		// returns the raw string since it isn't a JSON string either.
		{"object non-doc", json.RawMessage(`{"type":"other","content":[]}`), `{"type":"other","content":[]}`},
		// Bare number: not an ADF object and not a string -> returns raw.
		{"bare number", json.RawMessage(`42`), `42`},
		// ADF doc with an empty inline block (no text) is skipped entirely.
		{"adf empty block", json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[]}]}`), ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DescriptionToPlainText(tt.raw); got != tt.want {
				t.Errorf("DescriptionToPlainText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlainTextToADFEdgeBranches(t *testing.T) {
	t.Run("empty returns nil", func(t *testing.T) {
		if adf := PlainTextToADF(""); adf != nil {
			t.Errorf("expected nil for empty text, got %s", adf)
		}
	})

	t.Run("blank line yields empty paragraph", func(t *testing.T) {
		adf := PlainTextToADF("a\n\nb")
		var doc struct {
			Content []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"content"`
		}
		if err := json.Unmarshal(adf, &doc); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(doc.Content) != 3 {
			t.Fatalf("want 3 paragraphs, got %d", len(doc.Content))
		}
		// Middle paragraph is the blank line -> empty content slice.
		if len(doc.Content[1].Content) != 0 {
			t.Errorf("blank line should have empty content, got %d inline nodes", len(doc.Content[1].Content))
		}
	})
}

func TestPriorityToBeadsCustomMap(t *testing.T) {
	m := &jiraFieldMapper{priorityMap: map[string]string{"0": "P0-Critical"}}

	t.Run("custom map hit", func(t *testing.T) {
		if got := m.PriorityToBeads("P0-Critical"); got != 0 {
			t.Errorf("custom priority = %d, want 0", got)
		}
	})

	t.Run("non-string returns default", func(t *testing.T) {
		if got := m.PriorityToBeads(123); got != 2 {
			t.Errorf("non-string priority = %d, want 2", got)
		}
	})

	t.Run("out-of-range custom key ignored, falls to default", func(t *testing.T) {
		mm := &jiraFieldMapper{priorityMap: map[string]string{"9": "Weird"}}
		if got := mm.PriorityToBeads("Weird"); got != 2 {
			t.Errorf("out-of-range custom priority = %d, want default 2", got)
		}
	})
}

func TestStatusToTrackerCustomMapAndDefault(t *testing.T) {
	t.Run("custom map hit", func(t *testing.T) {
		m := &jiraFieldMapper{statusMap: map[string]string{string(types.StatusOpen): "Icebox"}}
		if got := m.StatusToTracker(types.StatusOpen); got != "Icebox" {
			t.Errorf("custom status = %v, want Icebox", got)
		}
	})

	t.Run("unknown status default", func(t *testing.T) {
		m := &jiraFieldMapper{}
		if got := m.StatusToTracker(types.Status("bogus")); got != "To Do" {
			t.Errorf("unknown status = %v, want To Do", got)
		}
	})
}

func TestFetchIssuesJQLVariants(t *testing.T) {
	var capturedJQL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/rest/api/3/search") {
			capturedJQL = r.URL.Query().Get("jql")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"issues": []Issue{}, "total": 0, "maxResults": 50, "startAt": 0,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	since := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)

	t.Run("multi-project IN clause + closed state + since", func(t *testing.T) {
		tr := &Tracker{
			client:      newTestClient(srv.URL, "3"),
			store:       &configStore{data: map[string]string{}},
			projectKeys: []string{"AAA", "BBB"},
			apiVersion:  "3",
		}
		if _, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{State: "closed", Since: &since}); err != nil {
			t.Fatalf("FetchIssues: %v", err)
		}
		if !strings.Contains(capturedJQL, "project IN (") {
			t.Errorf("expected IN clause, got %q", capturedJQL)
		}
		if !strings.Contains(capturedJQL, "statusCategory = Done") {
			t.Errorf("expected closed-state filter, got %q", capturedJQL)
		}
		if !strings.Contains(capturedJQL, "updated >=") {
			t.Errorf("expected since filter, got %q", capturedJQL)
		}
	})

	t.Run("search error propagates", func(t *testing.T) {
		errSrv := errServer(t, http.StatusBadRequest, "bad")
		tr := &Tracker{
			client:      newTestClient(errSrv.URL, "3"),
			store:       &configStore{data: map[string]string{}},
			projectKeys: []string{"AAA"},
			apiVersion:  "3",
		}
		if _, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{}); err == nil {
			t.Fatal("expected search error to propagate")
		}
	})
}

func TestApplyTransitionNoMatchingTransition(t *testing.T) {
	// GetIssueTransitions returns a transition that does NOT match the desired
	// status name, so applyTransition logs and returns nil (no TransitionIssue call).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/transitions") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(TransitionsResult{
				Transitions: []Transition{{ID: "31", To: StatusField{Name: "Some Other Status"}}},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	if err := tr.applyTransition(context.Background(), "PROJ-1", types.StatusClosed); err != nil {
		t.Fatalf("applyTransition should return nil when no match, got %v", err)
	}
}

func TestApplyTransitionGetTransitionsError(t *testing.T) {
	tr := newTrackerWithServer(errServer(t, http.StatusNotFound, "nope").URL, "3")
	if err := tr.applyTransition(context.Background(), "PROJ-1", types.StatusClosed); err == nil {
		t.Fatal("expected error when GetIssueTransitions fails")
	}
}
