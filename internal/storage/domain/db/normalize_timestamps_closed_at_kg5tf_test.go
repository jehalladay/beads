package db

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestNormalizeIssueTimestampsTruncatesClosedAndStartedAt is the teeth for
// beads-kg5tf: normalizeIssueTimestamps is the shared domain Insert/InsertBatch
// chokepoint (beads-82pv3) that truncates DATETIME timestamp fields to second
// precision so the in-memory struct the proxied-server create emits verbatim
// under --json matches the second-precision columns it was persisted into. 82pv3
// added created/updated/due/defer but MISSED closed_at and started_at — both are
// DATETIME columns (0001_create_issues.up.sql closed_at DATETIME; 0027 started_at
// DATETIME) emitted under --json (types.go closed_at/started_at ,omitempty). On
// the domain `bd create --status closed` path, closed_at is auto-computed from a
// raw-ns CreatedAt before this chokepoint, so an un-truncated (ns) closed_at was
// emitted while the column stored second precision — a read-after-write mismatch
// no later read could reproduce. This asserts both fields are truncated here.
func TestNormalizeIssueTimestampsTruncatesClosedAndStartedAt(t *testing.T) {
	// A time carrying sub-second nanoseconds — the exact value a raw time.Now()
	// (or CreatedAt.Add(time.Second) derived from one) would hold.
	ns := time.Date(2026, 7, 20, 15, 4, 5, 123456789, time.UTC)
	wantSec := ns.Truncate(time.Second)

	closed := ns
	started := ns
	issue := &types.Issue{
		Status:    types.StatusClosed,
		ClosedAt:  &closed,
		StartedAt: &started,
	}

	normalizeIssueTimestamps(issue)

	if issue.ClosedAt == nil {
		t.Fatal("ClosedAt became nil after normalize")
	}
	if got := *issue.ClosedAt; !got.Equal(wantSec) {
		t.Errorf("ClosedAt = %v (nanos %d), want second-truncated %v — an un-truncated closed_at is emitted under --json while the DATETIME column stores seconds (beads-kg5tf)",
			got, got.Nanosecond(), wantSec)
	}
	if got := issue.ClosedAt.Nanosecond(); got != 0 {
		t.Errorf("ClosedAt still carries %d nanoseconds; must be truncated to second precision", got)
	}

	if issue.StartedAt == nil {
		t.Fatal("StartedAt became nil after normalize")
	}
	if got := *issue.StartedAt; !got.Equal(wantSec) {
		t.Errorf("StartedAt = %v (nanos %d), want second-truncated %v (beads-kg5tf)",
			got, got.Nanosecond(), wantSec)
	}
	if got := issue.StartedAt.Nanosecond(); got != 0 {
		t.Errorf("StartedAt still carries %d nanoseconds; must be truncated to second precision", got)
	}
}

// TestNormalizeIssueTimestampsClosedAtNilStaysNil guards the nil-pointer path:
// an open issue has no closed_at/started_at, and normalize must not panic or
// fabricate one.
func TestNormalizeIssueTimestampsClosedAtNilStaysNil(t *testing.T) {
	issue := &types.Issue{Status: types.StatusOpen}
	normalizeIssueTimestamps(issue)
	if issue.ClosedAt != nil {
		t.Errorf("ClosedAt should stay nil for an open issue, got %v", *issue.ClosedAt)
	}
	if issue.StartedAt != nil {
		t.Errorf("StartedAt should stay nil when unset, got %v", *issue.StartedAt)
	}
}
