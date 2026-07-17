package notion

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestClientRetriesIdempotentOn429 verifies that an idempotent GET retries a
// 429 (rate limit) and succeeds once the server stops rate-limiting. Notion's
// API is aggressively rate-limited, so this is the common case. Retry-After: 0
// keeps the test fast (honors the server-mandated delay verbatim).
func TestClientRetriesIdempotentOn429(t *testing.T) {
	t.Parallel()

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"code":"rate_limited","message":"slow down"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"user-1","name":"Ada"}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	user, err := client.GetCurrentUser(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentUser returned error after 429 retry: %v", err)
	}
	if user.Name != "Ada" {
		t.Fatalf("user name = %q, want Ada", user.Name)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("server calls = %d, want 2 (one 429 + one success)", got)
	}
}

// TestClientRetriesIdempotentOn5xx verifies an idempotent GET retries a 503.
func TestClientRetriesIdempotentOn5xx(t *testing.T) {
	t.Parallel()

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"id":"user-1","name":"Ada"}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	if _, err := client.GetCurrentUser(context.Background()); err != nil {
		t.Fatalf("GetCurrentUser returned error after 503 retry: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("server calls = %d, want 2", got)
	}
}

// TestClientPostRetriesOn429 verifies that a POST create (non-idempotent) STILL
// retries a 429 — a rate limit is a clean pre-processing rejection, so nothing
// was written and a retry cannot mint a duplicate page (preserves the merm
// at-most-once contract while riding out rate limits).
func TestClientPostRetriesOn429(t *testing.T) {
	t.Parallel()

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"code":"rate_limited","message":"slow down"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"page-1"}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	page, err := client.CreatePage(context.Background(), "ds_123", map[string]interface{}{"Name": "x"})
	if err != nil {
		t.Fatalf("CreatePage returned error after 429 retry: %v", err)
	}
	if page.ID != "page-1" {
		t.Fatalf("page id = %q, want page-1", page.ID)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("server calls = %d, want 2 (429 is retryable for POST — clean rejection)", got)
	}
}

// TestClientPostDoesNotRetryOn5xx verifies that a POST create does NOT blind-
// retry a 5xx (the create may have committed before Notion errored). It returns
// an AmbiguousError so the caller reconciles instead of creating a duplicate.
func TestClientPostDoesNotRetryOn5xx(t *testing.T) {
	t.Parallel()

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":"internal_error","message":"boom"}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	_, err := client.CreatePage(context.Background(), "ds_123", map[string]interface{}{"Name": "x"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %v, want AmbiguousError (POST 5xx must not blind-retry)", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server calls = %d, want 1 (POST 5xx must NOT retry)", got)
	}
}

func TestClientRetrieveDataSourceSetsHeaders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/data_sources/ds_123" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("Notion-Version"); got != DefaultNotionVersion {
			t.Fatalf("notion version = %q", got)
		}
		_, _ = io.WriteString(w, `{"id":"ds_123","url":"https://www.notion.so/source","title":[{"plain_text":"Tasks"}],"properties":{"Name":{"type":"title"}}}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	ds, err := client.RetrieveDataSource(context.Background(), "ds_123")
	if err != nil {
		t.Fatalf("RetrieveDataSource returned error: %v", err)
	}
	if ds.ID != "ds_123" {
		t.Fatalf("id = %q", ds.ID)
	}
	if DataSourceTitle(ds.Title) != "Tasks" {
		t.Fatalf("title = %q", DataSourceTitle(ds.Title))
	}
}

func TestClientQueryDataSourcePaginates(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		switch r.Header.Get("X-Test-Step") {
		default:
		}
		if r.URL.Path != "/data_sources/ds_123/query" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		if !strings.Contains(r.URL.RawQuery, "") {
		}
		if strings.Contains(r.Header.Get("X-Page"), "2") {
		}
	}))
	defer server.Close()

	call := 0
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/data_sources/ds_123/query" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if call == 1 {
			if !strings.Contains(string(body), `"page_size":100`) {
				t.Fatalf("request body = %s", body)
			}
			_, _ = io.WriteString(w, `{"results":[{"id":"page-1"},{"id":"page-2"}],"has_more":true,"next_cursor":"cursor-2"}`)
			return
		}
		if !strings.Contains(string(body), `"start_cursor":"cursor-2"`) {
			t.Fatalf("request body = %s", body)
		}
		_, _ = io.WriteString(w, `{"results":[{"id":"page-3"}],"has_more":false}`)
	})

	client := NewClient("secret-token").WithBaseURL(server.URL)
	pages, err := client.QueryDataSource(context.Background(), "ds_123")
	if err != nil {
		t.Fatalf("QueryDataSource returned error: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("pages = %d, want 3", len(pages))
	}
}

func TestClientReturnsStructuredAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":"unauthorized","message":"token is invalid"}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	_, err := client.GetCurrentUser(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "token is invalid") {
		t.Fatalf("error = %q", err)
	}
}

func TestClientCreateDatabaseSendsInitialDataSource(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/databases" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		for _, want := range []string{
			`"page_id":"329e5bf9-7fae-8080-bb4a-d94e1387655d"`,
			`"initial_data_source"`,
			`"Beads ID"`,
			`"Status"`,
			`"Type"`,
		} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("request body missing %q\n%s", want, body)
			}
		}
		_, _ = io.WriteString(w, `{"id":"db_123","url":"https://www.notion.so/db123","data_sources":[{"id":"ds_123","name":"Beads Issues"}]}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	db, err := client.CreateDatabase(context.Background(), "329e5bf9-7fae-8080-bb4a-d94e1387655d", DefaultDatabaseTitle)
	if err != nil {
		t.Fatalf("CreateDatabase returned error: %v", err)
	}
	if db.ID != "db_123" {
		t.Fatalf("id = %q", db.ID)
	}
	if len(db.DataSources) != 1 || db.DataSources[0].ID != "ds_123" {
		t.Fatalf("data_sources = %+v", db.DataSources)
	}
}

func TestClientRetrieveDatabase(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/databases/db_123" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"db_123","url":"https://www.notion.so/db123","data_sources":[{"id":"ds_123","name":"Beads Issues"}]}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	db, err := client.RetrieveDatabase(context.Background(), "db_123")
	if err != nil {
		t.Fatalf("RetrieveDatabase returned error: %v", err)
	}
	if db.ID != "db_123" {
		t.Fatalf("id = %q", db.ID)
	}
	if len(db.DataSources) != 1 || db.DataSources[0].ID != "ds_123" {
		t.Fatalf("data_sources = %+v", db.DataSources)
	}
}

func TestResolveDataSourceReferencePrefersDataSource(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data_sources/329e5bf9-7fae-8080-bb4a-d94e1387655d":
			_, _ = io.WriteString(w, `{"id":"329e5bf9-7fae-8080-bb4a-d94e1387655d","properties":{"Name":{"type":"title"}}}`)
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	resolved, err := ResolveDataSourceReference(context.Background(), client, "https://www.notion.so/workspace/329e5bf97fae8080bb4ad94e1387655d")
	if err != nil {
		t.Fatalf("ResolveDataSourceReference returned error: %v", err)
	}
	if resolved.DataSourceID != "329e5bf9-7fae-8080-bb4a-d94e1387655d" {
		t.Fatalf("data_source_id = %q", resolved.DataSourceID)
	}
}

func TestResolveDataSourceReferenceFallsBackToDatabase(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data_sources/429e5bf9-7fae-8080-bb4a-d94e1387655d":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"object_not_found","message":"not found"}`)
		case "/databases/429e5bf9-7fae-8080-bb4a-d94e1387655d":
			_, _ = io.WriteString(w, `{"id":"429e5bf9-7fae-8080-bb4a-d94e1387655d","data_sources":[{"id":"529e5bf9-7fae-8080-bb4a-d94e1387655d","name":"Beads Issues"}]}`)
		case "/data_sources/529e5bf9-7fae-8080-bb4a-d94e1387655d":
			_, _ = io.WriteString(w, `{"id":"529e5bf9-7fae-8080-bb4a-d94e1387655d","properties":{"Name":{"type":"title"}}}`)
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	resolved, err := ResolveDataSourceReference(context.Background(), client, "https://www.notion.so/workspace/429e5bf97fae8080bb4ad94e1387655d")
	if err != nil {
		t.Fatalf("ResolveDataSourceReference returned error: %v", err)
	}
	if resolved.DataSourceID != "529e5bf9-7fae-8080-bb4a-d94e1387655d" {
		t.Fatalf("data_source_id = %q", resolved.DataSourceID)
	}
	if resolved.Database == nil || resolved.Database.ID != "429e5bf9-7fae-8080-bb4a-d94e1387655d" {
		t.Fatalf("database = %+v", resolved.Database)
	}
}
