package github

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestFieldMapperStatusToBeadsBranches(t *testing.T) {
	// StateMap hit: a configured mapping takes precedence over the switch.
	mapped := &githubFieldMapper{config: &MappingConfig{
		StateMap: map[string]string{"open": string(types.StatusInProgress)},
	}}
	if got := mapped.StatusToBeads("open"); got != types.StatusInProgress {
		t.Errorf("StatusToBeads(open) with StateMap = %q, want %q", got, types.StatusInProgress)
	}

	// Empty StateMap: falls through to the GitHub-specific switch defaults.
	empty := &githubFieldMapper{config: &MappingConfig{StateMap: map[string]string{}}}
	if got := empty.StatusToBeads("open"); got != types.StatusOpen {
		t.Errorf("StatusToBeads(open) switch fallback = %q, want %q", got, types.StatusOpen)
	}
	if got := empty.StatusToBeads("closed"); got != types.StatusClosed {
		t.Errorf("StatusToBeads(closed) switch fallback = %q, want %q", got, types.StatusClosed)
	}
	// Unknown state string with no mapping -> StatusOpen default.
	if got := empty.StatusToBeads("triaged"); got != types.StatusOpen {
		t.Errorf("StatusToBeads(unknown) = %q, want %q", got, types.StatusOpen)
	}

	// Non-string tracker state -> StatusOpen default (skips the type assert).
	if got := empty.StatusToBeads(42); got != types.StatusOpen {
		t.Errorf("StatusToBeads(non-string) = %q, want %q", got, types.StatusOpen)
	}
	if got := empty.StatusToBeads(nil); got != types.StatusOpen {
		t.Errorf("StatusToBeads(nil) = %q, want %q", got, types.StatusOpen)
	}
}

func TestExponentialBackoffBranches(t *testing.T) {
	// base <= 0 defaults to one second; attempt 0 -> base * 1.
	if got := exponentialBackoff(0, 0, 0); got != time.Second {
		t.Errorf("exponentialBackoff(0,0,0) = %v, want %v", got, time.Second)
	}

	// Normal growth: base * 2^attempt with no cap.
	if got := exponentialBackoff(time.Second, 3, 0); got != 8*time.Second {
		t.Errorf("exponentialBackoff(1s,3,0) = %v, want %v", got, 8*time.Second)
	}

	// maxBackoff caps the result.
	if got := exponentialBackoff(time.Second, 10, 5*time.Second); got != 5*time.Second {
		t.Errorf("exponentialBackoff cap = %v, want %v", got, 5*time.Second)
	}

	// attempt > 30 is clamped to 30 to avoid shift overflow; with a small cap
	// the clamped value still returns the cap rather than a negative/overflowed
	// duration.
	if got := exponentialBackoff(time.Second, 100, time.Minute); got != time.Minute {
		t.Errorf("exponentialBackoff(overflow-guard) = %v, want %v", got, time.Minute)
	}
}
