package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// graphqlServer spins up an httptest server that only answers /api/graphql and
// returns the given raw JSON as the GraphQL "data" (or errors) envelope.
func graphqlServer(t *testing.T, handler func(query string) (data string, errMsg string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/graphql") {
			t.Errorf("unexpected GraphQL path %q", r.URL.Path)
		}
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		data, errMsg := handler(body.Query)
		if errMsg != "" {
			w.Write([]byte(`{"errors":[{"message":` + strconv.Quote(errMsg) + `}]}`))
			return
		}
		w.Write([]byte(`{"data":` + data + `}`))
	}))
}

// TestGetTaskWorkItemTypeID covers the query-success path, the caching path,
// and the fallback-to-default on error.
func TestGetTaskWorkItemTypeID(t *testing.T) {
	t.Parallel()

	t.Run("resolves from query and caches", func(t *testing.T) {
		t.Parallel()
		var calls int
		server := graphqlServer(t, func(query string) (string, string) {
			calls++
			return `{"project":{"workItemTypes":{"nodes":[{"id":"gid://gitlab/WorkItems::Type/9"}]}}}`, ""
		})
		defer server.Close()

		client := NewClient("token", server.URL, "123")
		got := client.getTaskWorkItemTypeID(context.Background(), "grp/proj")
		if got != "gid://gitlab/WorkItems::Type/9" {
			t.Errorf("type id = %q, want the queried GID", got)
		}
		// Second call must hit the cache, not the server.
		_ = client.getTaskWorkItemTypeID(context.Background(), "grp/proj")
		if calls != 1 {
			t.Errorf("server calls = %d, want 1 (cached)", calls)
		}
	})

	t.Run("falls back to default on error", func(t *testing.T) {
		t.Parallel()
		server := graphqlServer(t, func(query string) (string, string) {
			return "", "boom"
		})
		defer server.Close()

		client := NewClient("token", server.URL, "123")
		got := client.getTaskWorkItemTypeID(context.Background(), "grp/proj")
		if got != defaultTaskTypeID {
			t.Errorf("type id = %q, want default %q", got, defaultTaskTypeID)
		}
	})
}

// TestGetWorkItemGID covers the found and not-found cases.
func TestGetWorkItemGID(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		server := graphqlServer(t, func(query string) (string, string) {
			return `{"project":{"workItems":{"nodes":[{"id":"gid://gitlab/WorkItem/77"}]}}}`, ""
		})
		defer server.Close()

		client := NewClient("token", server.URL, "123")
		gid, err := client.GetWorkItemGID(context.Background(), "grp/proj", 42)
		if err != nil {
			t.Fatalf("GetWorkItemGID error: %v", err)
		}
		if gid != "gid://gitlab/WorkItem/77" {
			t.Errorf("gid = %q, want the queried GID", gid)
		}
	})

	t.Run("not found errors", func(t *testing.T) {
		t.Parallel()
		server := graphqlServer(t, func(query string) (string, string) {
			return `{"project":{"workItems":{"nodes":[]}}}`, ""
		})
		defer server.Close()

		client := NewClient("token", server.URL, "123")
		_, err := client.GetWorkItemGID(context.Background(), "grp/proj", 42)
		if err == nil {
			t.Fatal("GetWorkItemGID(missing) = nil error, want not-found error")
		}
	})
}

// TestCreateTaskWorkItem covers creating a task (parent branch exercised),
// plus the GraphQL-errors-array failure path.
func TestCreateTaskWorkItem(t *testing.T) {
	t.Parallel()

	t.Run("success with parent", func(t *testing.T) {
		t.Parallel()
		var sawParent bool
		server := graphqlServer(t, func(query string) (string, string) {
			// The type-id lookup and the create mutation both arrive here;
			// answer each based on the query shape.
			if strings.Contains(query, "workItemTypes") {
				return `{"project":{"workItemTypes":{"nodes":[{"id":"gid://gitlab/WorkItems::Type/5"}]}}}`, ""
			}
			if strings.Contains(query, "parentId") {
				sawParent = true
			}
			return `{"workItemCreate":{"errors":[],"workItem":{"id":"gid://gitlab/WorkItem/1","iid":"3","title":"Task A","workItemType":{"name":"Task"},"webUrl":"u"}}}`, ""
		})
		defer server.Close()

		client := NewClient("token", server.URL, "123")
		wi, err := client.CreateTaskWorkItem(context.Background(), "grp/proj", "Task A", "body", "gid://gitlab/WorkItem/456")
		if err != nil {
			t.Fatalf("CreateTaskWorkItem error: %v", err)
		}
		if wi == nil || wi.Title != "Task A" || wi.Type != "Task" {
			t.Fatalf("got %+v, want Task A of type Task", wi)
		}
		if !sawParent {
			t.Error("expected the mutation to include the parentId hierarchy widget")
		}
	})

	t.Run("mutation errors array fails", func(t *testing.T) {
		t.Parallel()
		server := graphqlServer(t, func(query string) (string, string) {
			if strings.Contains(query, "workItemTypes") {
				return `{"project":{"workItemTypes":{"nodes":[{"id":"gid://gitlab/WorkItems::Type/5"}]}}}`, ""
			}
			return `{"workItemCreate":{"errors":["title taken"],"workItem":null}}`, ""
		})
		defer server.Close()

		client := NewClient("token", server.URL, "123")
		_, err := client.CreateTaskWorkItem(context.Background(), "grp/proj", "dup", "body", "")
		if err == nil {
			t.Fatal("CreateTaskWorkItem = nil error, want mutation-errors failure")
		}
	})
}
