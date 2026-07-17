// Additional pure-helper coverage for tracker engine helpers that carried
// partial coverage (beads-4vtv): parseSyncTime, marshalTrackerMetadata,
// formatRateLimitWait, and the no-DB fallback branch of externalRefChangedAfter.
// Pure lane — no `//go:build cgo`, no sql.DB, reuses newPureTestStore +
// fakeRateLimitError from the shared test helpers.
package tracker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestParseSyncTime(t *testing.T) {
	nano := "2026-07-17T10:11:12.123456789Z"
	sec := "2026-07-17T10:11:12Z"

	if _, err := parseSyncTime(""); err == nil {
		t.Fatal("parseSyncTime(\"\") = nil error, want error")
	}

	got, err := parseSyncTime(nano)
	if err != nil {
		t.Fatalf("parseSyncTime(RFC3339Nano) error: %v", err)
	}
	if !got.Equal(time.Date(2026, 7, 17, 10, 11, 12, 123456789, time.UTC)) {
		t.Fatalf("parseSyncTime(RFC3339Nano) = %v", got)
	}

	got, err = parseSyncTime(sec)
	if err != nil {
		t.Fatalf("parseSyncTime(RFC3339) error: %v", err)
	}
	if !got.Equal(time.Date(2026, 7, 17, 10, 11, 12, 0, time.UTC)) {
		t.Fatalf("parseSyncTime(RFC3339) = %v", got)
	}

	if _, err := parseSyncTime("not-a-timestamp"); err == nil {
		t.Fatal("parseSyncTime(invalid) = nil error, want error")
	}
}

func TestMarshalTrackerMetadata(t *testing.T) {
	if raw, ok := marshalTrackerMetadata(nil); ok || raw != nil {
		t.Fatalf("marshalTrackerMetadata(nil) = (%q, %v), want (nil, false)", raw, ok)
	}

	raw, ok := marshalTrackerMetadata(map[string]string{"k": "v"})
	if !ok {
		t.Fatal("marshalTrackerMetadata(map) ok = false, want true")
	}
	if string(raw) != `{"k":"v"}` {
		t.Fatalf("marshalTrackerMetadata(map) = %s", raw)
	}

	// A func value is not JSON-marshalable → err path → (nil, false).
	if raw, ok := marshalTrackerMetadata(func() {}); ok || raw != nil {
		t.Fatalf("marshalTrackerMetadata(func) = (%q, %v), want (nil, false)", raw, ok)
	}
}

func TestFormatRateLimitWait(t *testing.T) {
	// Non-rate-limit error → "unknown".
	if got := formatRateLimitWait(errors.New("boom")); got != "unknown" {
		t.Fatalf("formatRateLimitWait(plain err) = %q, want unknown", got)
	}

	// Rate-limit error with zero retry-after → "unknown".
	if got := formatRateLimitWait(&fakeRateLimitError{retryAfter: 0, msg: "rl"}); got != "unknown" {
		t.Fatalf("formatRateLimitWait(zero) = %q, want unknown", got)
	}

	// Rate-limit error with negative retry-after → "unknown".
	if got := formatRateLimitWait(&fakeRateLimitError{retryAfter: -5 * time.Second, msg: "rl"}); got != "unknown" {
		t.Fatalf("formatRateLimitWait(negative) = %q, want unknown", got)
	}

	// Positive retry-after → rounded message.
	got := formatRateLimitWait(&fakeRateLimitError{retryAfter: 90 * time.Second, msg: "rl"})
	if got != "retry after 1m30s" {
		t.Fatalf("formatRateLimitWait(90s) = %q, want \"retry after 1m30s\"", got)
	}
}

func TestExternalRefChangedAfterNilLocal(t *testing.T) {
	eng := &Engine{Store: newPureTestStore()}
	changed, err := eng.externalRefChangedAfter(context.Background(), nil, "ref", time.Now())
	if err != nil {
		t.Fatalf("externalRefChangedAfter(nil local) error: %v", err)
	}
	if changed {
		t.Fatal("externalRefChangedAfter(nil local) = true, want false")
	}
}

func TestExternalRefChangedAfterNoDBFallback(t *testing.T) {
	// pureTestStore has no DB() method → the no-dbProvider fallback branch:
	// changed == (CreatedAt.After(lastSync) || UpdatedAt.After(lastSync)).
	lastSync := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	eng := &Engine{Store: newPureTestStore()}
	ctx := context.Background()

	// Both timestamps before lastSync → not changed.
	before := &types.Issue{
		ID:        "bd-1",
		CreatedAt: lastSync.Add(-2 * time.Hour),
		UpdatedAt: lastSync.Add(-1 * time.Hour),
	}
	if changed, err := eng.externalRefChangedAfter(ctx, before, "ref", lastSync); err != nil || changed {
		t.Fatalf("externalRefChangedAfter(before) = (%v, %v), want (false, nil)", changed, err)
	}

	// UpdatedAt after lastSync → changed.
	updated := &types.Issue{
		ID:        "bd-2",
		CreatedAt: lastSync.Add(-2 * time.Hour),
		UpdatedAt: lastSync.Add(1 * time.Hour),
	}
	if changed, err := eng.externalRefChangedAfter(ctx, updated, "ref", lastSync); err != nil || !changed {
		t.Fatalf("externalRefChangedAfter(updated) = (%v, %v), want (true, nil)", changed, err)
	}

	// CreatedAt after lastSync → changed.
	created := &types.Issue{
		ID:        "bd-3",
		CreatedAt: lastSync.Add(1 * time.Hour),
		UpdatedAt: lastSync.Add(-1 * time.Hour),
	}
	if changed, err := eng.externalRefChangedAfter(ctx, created, "ref", lastSync); err != nil || !changed {
		t.Fatalf("externalRefChangedAfter(created) = (%v, %v), want (true, nil)", changed, err)
	}
}
