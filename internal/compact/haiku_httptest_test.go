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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

// withRecordingTracer installs a global OTel tracer provider backed by an
// in-memory span recorder for the duration of the test, then restores the
// previous provider. telemetry.Tracer() resolves through the global provider,
// so this captures the spans callWithRetry produces.
func withRecordingTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})
	return sr
}

// findAnthropicSpan returns the recorded "anthropic.messages.new" span, or
// fails the test if it is absent.
func findAnthropicSpan(t *testing.T, sr *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range sr.Ended() {
		if s.Name() == "anthropic.messages.new" {
			return s
		}
	}
	t.Fatalf("no anthropic.messages.new span recorded (got %d spans)", len(sr.Ended()))
	return nil
}

// TestCallWithRetry_MalformedContentRecordsSpanError proves the span-error
// contract for the two "unexpected response format" paths: a 200 response with
// no content blocks (or a non-text first block) must leave the span marked
// Error with the error recorded — matching every other error path in
// callWithRetry (regression guard for beads-nv7p).
func TestCallWithRetry_MalformedContentRecordsSpanError(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "no content blocks",
			body: `{"id":"m","type":"message","role":"assistant","model":"claude-test-model",
				"content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":0}}`,
			wantErr: "no content blocks",
		},
		{
			name: "first block not text",
			body: `{"id":"m","type":"message","role":"assistant","model":"claude-test-model",
				"content":[{"type":"thinking","thinking":"hmm","signature":"x"}],
				"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":0}}`,
			wantErr: "not a text block",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			sr := withRecordingTracer(t)
			h := newTestHaikuClient(t, srv.URL)

			_, err := h.callWithRetry(context.Background(), "prompt")
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want to contain %q", err, tc.wantErr)
			}

			span := findAnthropicSpan(t, sr)
			if got := span.Status().Code; got != codes.Error {
				t.Errorf("span status code = %v, want Error", got)
			}
			var recorded bool
			for _, e := range span.Events() {
				if e.Name == "exception" {
					recorded = true
					break
				}
			}
			if !recorded {
				t.Error("span has no recorded exception event; expected span.RecordError on the malformed-content path")
			}
		})
	}
}
