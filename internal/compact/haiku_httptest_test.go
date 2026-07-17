package compact

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newTestHaikuClient builds a haikuClient whose Anthropic client points at the
// given test server base URL. It mirrors newHaikuClient's construction without
// touching the real API, env, or config.
func newTestHaikuClient(t *testing.T, baseURL string) *haikuClient {
	t.Helper()
	client := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(baseURL),
		option.WithMaxRetries(0), // isolate callWithRetry's loop from the SDK's own retries
	)
	tmpl, err := template.New("tier1").Parse(tier1PromptTemplate)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	return &haikuClient{
		client:         client,
		model:          "claude-test-model",
		tier1Template:  tmpl,
		maxRetries:     maxRetries,
		initialBackoff: 1 * time.Millisecond, // keep retries fast
	}
}

// textMessageJSON is a minimal valid Messages API response with a text block.
func textMessageJSON(text string) string {
	return `{
		"id": "msg_test",
		"type": "message",
		"role": "assistant",
		"model": "claude-test-model",
		"content": [{"type": "text", "text": "` + text + `"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`
}

func TestCallWithRetry_SuccessFirstAttempt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(textMessageJSON("hello summary")))
	}))
	defer srv.Close()

	h := newTestHaikuClient(t, srv.URL)
	got, err := h.callWithRetry(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello summary" {
		t.Errorf("callWithRetry() = %q, want %q", got, "hello summary")
	}
}

func TestCallWithRetry_RetryThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			// First attempt: retryable 500.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(textMessageJSON("recovered")))
	}))
	defer srv.Close()

	h := newTestHaikuClient(t, srv.URL)
	got, err := h.callWithRetry(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "recovered" {
		t.Errorf("callWithRetry() = %q, want %q", got, "recovered")
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Errorf("expected at least 2 calls (retry), got %d", calls)
	}
}

func TestCallWithRetry_NonRetryable(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		// 400 is non-retryable.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer srv.Close()

	h := newTestHaikuClient(t, srv.URL)
	_, err := h.callWithRetry(context.Background(), "prompt")
	if err == nil {
		t.Fatal("expected non-retryable error")
	}
	if !strings.Contains(err.Error(), "non-retryable error") {
		t.Errorf("error = %v, want to contain 'non-retryable error'", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("non-retryable should call once, got %d", got)
	}
}

func TestCallWithRetry_ExhaustsRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"always fails"}}`))
	}))
	defer srv.Close()

	h := newTestHaikuClient(t, srv.URL)
	h.maxRetries = 2 // 3 total attempts
	_, err := h.callWithRetry(context.Background(), "prompt")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "failed after") {
		t.Errorf("error = %v, want to contain 'failed after'", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 attempts (maxRetries=2), got %d", got)
	}
}

func TestCallWithRetry_EmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"model": "claude-test-model",
			"content": [],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 0}
		}`))
	}))
	defer srv.Close()

	h := newTestHaikuClient(t, srv.URL)
	_, err := h.callWithRetry(context.Background(), "prompt")
	if err == nil {
		t.Fatal("expected error for empty content blocks")
	}
	if !strings.Contains(err.Error(), "no content blocks") {
		t.Errorf("error = %v, want to contain 'no content blocks'", err)
	}
}
