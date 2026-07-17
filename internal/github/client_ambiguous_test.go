package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestCreateIssue_5xx_NotRetried_NoDuplicate is the beads-merm regression: a
// POST (create) that hits a 5xx must NOT be retried, because GitHub may have
// written the issue before erroring — a retry would mint a duplicate. Exactly
// one create attempt must reach the server, and the caller gets an
// *AmbiguousError.
func TestCreateIssue_5xx_NotRetried_NoDuplicate(t *testing.T) {
	var creates int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&creates, 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"server error"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := fastRetryTestClient(srv.URL)
	_, err := c.CreateIssue(context.Background(), "title", "body", nil)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := atomic.LoadInt32(&creates); got != 1 {
		t.Fatalf("create POST sent %d times, want exactly 1 (retry would duplicate)", got)
	}
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("expected *AmbiguousError, got %T: %v", err, err)
	}
}

// TestCreateIssue_TransportError_NotRetried is the transport-error arm: a lost
// connection on the create POST is ambiguous and must not be retried.
func TestCreateIssue_TransportError_NotRetried(t *testing.T) {
	var creates int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&creates, 1)
		// Hijack and close without responding → transport error on the client.
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	c := fastRetryTestClient(srv.URL)
	_, err := c.CreateIssue(context.Background(), "title", "body", nil)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := atomic.LoadInt32(&creates); got != 1 {
		t.Fatalf("create POST sent %d times, want exactly 1 (retry would duplicate)", got)
	}
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("expected *AmbiguousError, got %T: %v", err, err)
	}
}

// TestUpdateIssue_5xx_StillRetries confirms the guard is scoped to POST:
// UpdateIssue (PATCH) is idempotent and keeps retrying on 5xx.
func TestUpdateIssue_5xx_StillRetries(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number":1,"title":"t"}`))
	}))
	defer srv.Close()

	c := fastRetryTestClient(srv.URL)
	_, err := c.UpdateIssue(context.Background(), 1, map[string]interface{}{"title": "t"})
	if err != nil {
		t.Fatalf("PATCH should retry through transient 5xx and succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got < 3 {
		t.Errorf("expected PATCH to retry (>=3 attempts), got %d", got)
	}
}
