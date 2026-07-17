package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestCreateIssue_5xx_NotRetried_NoDuplicate is the beads-merm regression: a
// POST (create) that hits a 5xx must NOT be retried, because GitLab may have
// written the issue before erroring — a retry would mint a duplicate. Exactly
// one create attempt must reach the server, and the caller gets an
// *AmbiguousError.
func TestCreateIssue_5xx_NotRetried_NoDuplicate(t *testing.T) {
	var creates int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&creates, 1)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL, "123")
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
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL, "123")
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

// TestCreateIssue_429_StillRetries confirms a rate limit is a clean rejection:
// GitLab throttled the POST without processing it, so a retry cannot
// duplicate. Retry-After:0 keeps the test fast.
func TestCreateIssue_429_StillRetries(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"iid":1,"title":"t"}`))
	}))
	defer srv.Close()

	c := NewClient("token", srv.URL, "123")
	_, err := c.CreateIssue(context.Background(), "title", "body", nil)
	if err != nil {
		t.Fatalf("429 is a clean rejection; POST should retry and succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got < 2 {
		t.Errorf("expected POST to retry past 429 (>=2 attempts), got %d", got)
	}
}
