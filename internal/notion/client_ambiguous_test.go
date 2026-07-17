package notion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestCreatePage_5xx_NotRetried_NoDuplicate is the beads-merm invariant for
// Notion: the client's doRequest has NO retry loop, so a create POST that hits
// a 5xx is sent exactly once — it can never mint a duplicate page. This test
// locks that in so a future "add retries" change cannot silently reintroduce
// the duplicate-create hazard on the non-idempotent create path.
func TestCreatePage_5xx_NotRetried_NoDuplicate(t *testing.T) {
	var creates int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&creates, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"service_unavailable","message":"try later"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	_, err := client.CreatePage(context.Background(), "ds_123", map[string]interface{}{"Name": "x"})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := atomic.LoadInt32(&creates); got != 1 {
		t.Fatalf("create POST sent %d times, want exactly 1 (retry would duplicate)", got)
	}
}
