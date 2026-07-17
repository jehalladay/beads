package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// errStatusServer answers every request with the given non-retriable status
// and body, so client error paths return immediately (no retry backoff).
func errStatusServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// okBodyServer answers every request 200 with a fixed raw body (used to feed
// malformed JSON into the parse-error branches).
func okBodyServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

func TestCreateMilestone_RequestError(t *testing.T) {
	t.Parallel()
	srv := errStatusServer(t, http.StatusBadRequest, `{"message":"bad"}`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	if _, err := c.CreateMilestone(context.Background(), "v1", "desc"); err == nil {
		t.Fatal("expected error from CreateMilestone on 400")
	}
}

func TestCreateMilestone_BadJSON(t *testing.T) {
	t.Parallel()
	srv := okBodyServer(t, `{not json`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	if _, err := c.CreateMilestone(context.Background(), "v1", "desc"); err == nil {
		t.Fatal("expected parse error from CreateMilestone")
	}
}

func TestUpdateMilestone_RequestError(t *testing.T) {
	t.Parallel()
	srv := errStatusServer(t, http.StatusNotFound, `{"message":"missing"}`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	if _, err := c.UpdateMilestone(context.Background(), 7, map[string]interface{}{"title": "v2"}); err == nil {
		t.Fatal("expected error from UpdateMilestone on 404")
	}
}

func TestUpdateMilestone_BadJSON(t *testing.T) {
	t.Parallel()
	srv := okBodyServer(t, `[bad`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	if _, err := c.UpdateMilestone(context.Background(), 7, map[string]interface{}{"title": "v2"}); err == nil {
		t.Fatal("expected parse error from UpdateMilestone")
	}
}

func TestListProjects_RequestError(t *testing.T) {
	t.Parallel()
	srv := errStatusServer(t, http.StatusForbidden, `{"message":"denied"}`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	if _, err := c.ListProjects(context.Background()); err == nil {
		t.Fatal("expected error from ListProjects on 403")
	}
}

func TestListProjects_BadJSON(t *testing.T) {
	t.Parallel()
	srv := okBodyServer(t, `{not an array`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	if _, err := c.ListProjects(context.Background()); err == nil {
		t.Fatal("expected parse error from ListProjects")
	}
}

func TestListProjects_Success(t *testing.T) {
	t.Parallel()
	srv := okBodyServer(t, `[{"id":1,"name":"proj-a"},{"id":2,"name":"proj-b"}]`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	projects, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2", len(projects))
	}
}

// graphqlRequest surfaces a request error (non-2xx) from doRequest.
func TestGraphqlRequest_RequestError(t *testing.T) {
	t.Parallel()
	srv := errStatusServer(t, http.StatusBadRequest, `{"message":"bad"}`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	if _, err := c.graphqlRequest(context.Background(), "query{}", nil); err == nil {
		t.Fatal("expected request error from graphqlRequest on 400")
	}
}

// graphqlRequest returns a parse error when the envelope is malformed JSON.
func TestGraphqlRequest_BadJSON(t *testing.T) {
	t.Parallel()
	srv := okBodyServer(t, `{not json`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	if _, err := c.graphqlRequest(context.Background(), "query{}", nil); err == nil {
		t.Fatal("expected parse error from graphqlRequest")
	}
}

// graphqlRequest converts a populated GraphQL "errors" array into a Go error.
func TestGraphqlRequest_GraphQLErrors(t *testing.T) {
	t.Parallel()
	srv := okBodyServer(t, `{"errors":[{"message":"field does not exist"}]}`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	_, err := c.graphqlRequest(context.Background(), "query{}", nil)
	if err == nil {
		t.Fatal("expected GraphQL error from populated errors array")
	}
}

// graphqlRequest with variables exercises the variables-population branch and
// returns the data envelope on success.
func TestGraphqlRequest_WithVariablesSuccess(t *testing.T) {
	t.Parallel()
	srv := okBodyServer(t, `{"data":{"ok":true}}`)
	defer srv.Close()
	c := NewClient("token", srv.URL, "123")
	data, err := c.graphqlRequest(context.Background(), "query($x:ID){}", map[string]interface{}{"x": "1"})
	if err != nil {
		t.Fatalf("graphqlRequest: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty data envelope")
	}
}
