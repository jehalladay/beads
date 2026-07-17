package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A transient 5xx must be retried; a subsequent 2xx returns the good body.
// Exercises doRequest's transient-error switch (500) + backoff sleep + the
// eventual success return that earlier tests skipped.
func TestDoRequest_TransientThenSuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"message":"try later"}`))
			return
		}
		writeJSON(t, w, []Issue{{Number: 42, Title: "ok"}})
	}))
	defer srv.Close()

	c := fastRetryTestClient(srv.URL)
	body, _, err := c.doRequest(context.Background(), http.MethodGet, srv.URL+"/x", nil)
	if err != nil {
		t.Fatalf("doRequest after retry: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("expected 2 attempts (1 transient + 1 success), got %d", got)
	}
	if len(body) == 0 {
		t.Error("expected a non-empty body on success")
	}
}

// A persistent 5xx exhausts retries and surfaces the last transient error via
// the max-retries-exceeded return.
func TestDoRequest_TransientExhaustsRetries(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"still down"}`))
	}))
	defer srv.Close()

	c := fastRetryTestClient(srv.URL)
	_, _, err := c.doRequest(context.Background(), http.MethodGet, srv.URL+"/x", nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// MaxRetries=3 => 4 total attempts.
	if got := atomic.LoadInt32(&attempts); got != 4 {
		t.Errorf("expected 4 attempts, got %d", got)
	}
}

// A non-retryable, non-auth status (404) returns the generic API error
// immediately without retrying — exercises the terminal switch fall-through.
func TestDoRequest_NotFoundReturnsAPIError(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	}))
	defer srv.Close()

	c := fastRetryTestClient(srv.URL)
	_, _, err := c.doRequest(context.Background(), http.MethodGet, srv.URL+"/x", nil)
	if err == nil {
		t.Fatal("expected API error for 404")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("404 must not retry, got %d attempts", got)
	}
}

// A cancelled context before the transient backoff sleep completes makes
// doRequest return the context error from sleep().
func TestDoRequest_ContextCancelledDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"down"}`))
	}))
	defer srv.Close()

	c := newRateLimitTestClient(srv.URL)
	c.Retry = RetryConfig{MaxRetries: 3, BaseDelay: time.Hour, MaxBackoff: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: sleep() returns ctx.Err() on the first backoff
	_, _, err := c.doRequest(ctx, http.MethodGet, srv.URL+"/x", nil)
	if err == nil {
		t.Fatal("expected context error during backoff")
	}
}

// FetchIssuesSince across two pages: page 1 carries a Link:next header and a
// PR that must be filtered out; page 2 has one real issue and no next link.
func TestFetchIssuesSince_PaginatesAndFiltersPRs(t *testing.T) {
	var base string
	var page2Seen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, base))
			writeJSON(t, w, []Issue{
				{Number: 1, Title: "real"},
				{Number: 2, Title: "pr", PullRequest: &PullRequestRef{URL: "x"}},
			})
		default:
			page2Seen = true
			writeJSON(t, w, []Issue{{Number: 3, Title: "page2"}})
		}
	}))
	defer srv.Close()
	base = srv.URL

	c := newMethodTestClient(srv.URL)
	issues, err := c.FetchIssuesSince(context.Background(), "all", mustTime(t, "2026-01-01T00:00:00Z"))
	if err != nil {
		t.Fatalf("FetchIssuesSince: %v", err)
	}
	if !page2Seen {
		t.Error("expected page 2 to be fetched via Link:next")
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 non-PR issues across 2 pages, got %d: %+v", len(issues), issues)
	}
	if issues[0].Number != 1 || issues[1].Number != 3 {
		t.Errorf("unexpected issue numbers: %d, %d", issues[0].Number, issues[1].Number)
	}
}

// FetchIssuesSince surfaces a transport/API error from doRequest.
func TestFetchIssuesSince_RequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"gone"}`))
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	_, err := c.FetchIssuesSince(context.Background(), "open", time.Now())
	if err == nil {
		t.Fatal("expected error from FetchIssuesSince on 404")
	}
}

// FetchIssuesSince returns a parse error on malformed JSON.
func TestFetchIssuesSince_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	_, err := c.FetchIssuesSince(context.Background(), "open", time.Now())
	if err == nil {
		t.Fatal("expected parse error from FetchIssuesSince")
	}
}

// FetchIssuesSince honours a cancelled context at the top of the loop.
func TestFetchIssuesSince_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []Issue{{Number: 1}})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newMethodTestClient(srv.URL)
	_, err := c.FetchIssuesSince(ctx, "open", time.Now())
	if err == nil {
		t.Fatal("expected context error")
	}
}

// ListRepositories across two pages, then no next link.
func TestListRepositories_Paginates(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, base))
			writeJSON(t, w, []Repository{{ID: 1, Name: "a", FullName: "owner/a"}})
		default:
			writeJSON(t, w, []Repository{{ID: 2, Name: "b", FullName: "owner/b"}})
		}
	}))
	defer srv.Close()
	base = srv.URL

	c := newMethodTestClient(srv.URL)
	repos, err := c.ListRepositories(context.Background())
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos across 2 pages, got %d", len(repos))
	}
	if repos[0].Name != "a" || repos[1].Name != "b" {
		t.Errorf("unexpected repo names: %q, %q", repos[0].Name, repos[1].Name)
	}
}

// ListRepositories surfaces an API error from doRequest.
func TestListRepositories_RequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"gone"}`))
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	_, err := c.ListRepositories(context.Background())
	if err == nil {
		t.Fatal("expected error from ListRepositories on 404")
	}
}

// ListRepositories returns a parse error on malformed JSON.
func TestListRepositories_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[not json`))
	}))
	defer srv.Close()

	c := newMethodTestClient(srv.URL)
	_, err := c.ListRepositories(context.Background())
	if err == nil {
		t.Fatal("expected parse error from ListRepositories")
	}
}

// ListRepositories honours a cancelled context at the top of the loop.
func TestListRepositories_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []Repository{{ID: 1}})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newMethodTestClient(srv.URL)
	_, err := c.ListRepositories(ctx)
	if err == nil {
		t.Fatal("expected context error")
	}
}
