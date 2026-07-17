package github

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestAuthError_Error(t *testing.T) {
	withMsg := (&AuthError{StatusCode: 403, Message: "Bad credentials"}).Error()
	if !strings.Contains(withMsg, "403") || !strings.Contains(withMsg, "Bad credentials") {
		t.Errorf("AuthError with message = %q", withMsg)
	}
	noMsg := (&AuthError{StatusCode: 401}).Error()
	if !strings.Contains(noMsg, "401") || strings.Contains(noMsg, ": ") {
		t.Errorf("AuthError without message = %q", noMsg)
	}
}

func TestRateLimitErrorKind_String(t *testing.T) {
	tests := []struct {
		kind RateLimitErrorKind
		want string
	}{
		{RateLimitPrimary, "primary"},
		{RateLimitSecondary, "secondary"},
		{RateLimitErrorKind(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestRateLimitError_RateLimitRetryAfter(t *testing.T) {
	// Explicit Retry-After wins.
	if got := (&RateLimitError{RetryAfter: 30 * time.Second}).RateLimitRetryAfter(); got != 30*time.Second {
		t.Errorf("explicit retry-after = %v, want 30s", got)
	}
	// Primary with a future reset returns time-until-reset (> 0).
	future := &RateLimitError{Kind: RateLimitPrimary, ResetAt: time.Now().Add(time.Hour)}
	if got := future.RateLimitRetryAfter(); got <= 0 {
		t.Errorf("primary future reset = %v, want > 0", got)
	}
	// Primary with a past reset falls through to 0 (not secondary).
	past := &RateLimitError{Kind: RateLimitPrimary, ResetAt: time.Now().Add(-time.Hour)}
	if got := past.RateLimitRetryAfter(); got != 0 {
		t.Errorf("primary past reset = %v, want 0", got)
	}
	// Secondary with no signals returns the 60s floor.
	if got := (&RateLimitError{Kind: RateLimitSecondary}).RateLimitRetryAfter(); got != 60*time.Second {
		t.Errorf("secondary floor = %v, want 60s", got)
	}
}

func TestRateLimitError_Error(t *testing.T) {
	e := &RateLimitError{
		Kind:       RateLimitPrimary,
		StatusCode: 403,
		RetryAfter: 5 * time.Second,
		ResetAt:    time.Unix(1700000000, 0),
		Resource:   "core",
		Message:    "API rate limit exceeded",
	}
	got := e.Error()
	for _, want := range []string{"primary", "403", "retry-after=5s", "reset-at=", "resource=core", "API rate limit exceeded"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, missing %q", got, want)
		}
	}
	// Minimal error omits the optional segments.
	min := (&RateLimitError{Kind: RateLimitSecondary, StatusCode: 429}).Error()
	if strings.Contains(min, "retry-after") || strings.Contains(min, "resource=") {
		t.Errorf("minimal Error() leaked optional fields: %q", min)
	}
}

func TestExtractGitHubMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", ""},
		{"json message", `{"message":"Not Found"}`, "Not Found"},
		{"malformed json falls back to raw", `{bad json`, "{bad json"},
		{"non-json raw", "plain text error", "plain text error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractGitHubMessage([]byte(tt.body)); got != tt.want {
				t.Errorf("extractGitHubMessage(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}

	// Long non-JSON bodies are clamped with an ellipsis.
	long := strings.Repeat("x", 500)
	got := extractGitHubMessage([]byte(long))
	if !strings.HasSuffix(got, "…") || len(got) > 210 {
		t.Errorf("long body not clamped: len=%d suffix=%q", len(got), got[len(got)-3:])
	}
}

func TestFieldMapper_PriorityAndStatusToBeads(t *testing.T) {
	m := &githubFieldMapper{config: DefaultMappingConfig()}

	// PriorityToBeads: known label -> configured value; non-string / unknown -> 2.
	for label, want := range m.config.PriorityMap {
		if got := m.PriorityToBeads(label); got != want {
			t.Errorf("PriorityToBeads(%q) = %d, want %d", label, got, want)
		}
		break // one round-trip is enough to hit the mapped arm
	}
	if got := m.PriorityToBeads("nonexistent-label"); got != 2 {
		t.Errorf("PriorityToBeads(unknown) = %d, want 2", got)
	}
	if got := m.PriorityToBeads(12345); got != 2 {
		t.Errorf("PriorityToBeads(non-string) = %d, want 2", got)
	}

	// StatusToBeads: GitHub-native "open"/"closed" defaults + non-string default.
	if got := m.StatusToBeads("open"); got != types.StatusOpen {
		t.Errorf("StatusToBeads(open) = %v", got)
	}
	if got := m.StatusToBeads("closed"); got != types.StatusClosed {
		t.Errorf("StatusToBeads(closed) = %v", got)
	}
	if got := m.StatusToBeads(42); got != types.StatusOpen {
		t.Errorf("StatusToBeads(non-string) = %v, want open", got)
	}
}

func TestFieldMapper_IssueToTracker(t *testing.T) {
	m := &githubFieldMapper{config: DefaultMappingConfig()}
	issue := &types.Issue{ID: "bd-1", Title: "t", Priority: 1, Status: types.StatusClosed}
	fields := m.IssueToTracker(issue)
	if fields == nil {
		t.Fatal("IssueToTracker returned nil")
	}
}
