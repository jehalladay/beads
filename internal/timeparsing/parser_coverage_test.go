package timeparsing

import (
	"testing"
	"time"
)

// applyDuration's default arm is unreachable through ParseCompactDuration (the
// regex constrains the unit to [hdwmy]), so exercise it directly to prove it
// returns the base time unchanged for an unknown unit.
func TestApplyDuration_UnknownUnitReturnsBase(t *testing.T) {
	base := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	got := applyDuration(base, 5, "q")
	if !got.Equal(base) {
		t.Fatalf("applyDuration(unknown unit) = %v, want unchanged base %v", got, base)
	}
}

func TestApplyDuration_AllUnits(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		unit string
		amt  int
		want time.Time
	}{
		{"h", 6, base.Add(6 * time.Hour)},
		{"d", 2, base.AddDate(0, 0, 2)},
		{"w", 1, base.AddDate(0, 0, 7)},
		{"m", 3, base.AddDate(0, 3, 0)},
		{"y", 1, base.AddDate(1, 0, 0)},
	}
	for _, c := range cases {
		if got := applyDuration(base, c.amt, c.unit); !got.Equal(c.want) {
			t.Errorf("applyDuration(%d%s) = %v, want %v", c.amt, c.unit, got, c.want)
		}
	}
}

// parseNaturalLanguage must wrap a non-time expression as an error rather than
// returning a zero time silently.
func TestParseNaturalLanguage_NonTimeExpression(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	if _, err := parseNaturalLanguage("this is not a time at all zzzz", now); err == nil {
		t.Fatal("want error for non-time expression, got nil")
	}
}

// ParseRelativeTime's absolute-format layer has two arms the existing tests
// don't hit: ISO 8601 without timezone, and a space-separated datetime.
func TestParseRelativeTime_ISO8601NoTimezone(t *testing.T) {
	now := time.Now()
	got, err := ParseRelativeTime("2026-03-15T09:30:00", now)
	if err != nil {
		t.Fatalf("ParseRelativeTime(ISO8601-no-tz) error: %v", err)
	}
	want := time.Date(2026, 3, 15, 9, 30, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseRelativeTime_SpaceSeparatedDatetime(t *testing.T) {
	now := time.Now()
	got, err := ParseRelativeTime("2026-03-15 09:30:00", now)
	if err != nil {
		t.Fatalf("ParseRelativeTime(space datetime) error: %v", err)
	}
	want := time.Date(2026, 3, 15, 9, 30, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestParseRelativeTime_InvalidDateOnlyRejected covers beads-atue: a string
// matching YYYY-MM-DD but denoting an invalid calendar date must ERROR, not
// fall through to the NLP layer (which used to misread the tail as a time-of-
// today and silently return a nonsense deadline).
func TestParseRelativeTime_InvalidDateOnlyRejected(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	for _, in := range []string{"2025-13-45", "2025-02-30", "2025-00-10", "2025-01-32"} {
		if got, err := ParseRelativeTime(in, now); err == nil {
			t.Errorf("ParseRelativeTime(%q) = %v, want error (invalid date must not fall through to NLP)", in, got)
		}
	}

	// Valid date-only still parses (regression guard for the early-return path).
	got, err := ParseRelativeTime("2025-01-15", now)
	if err != nil {
		t.Fatalf("ParseRelativeTime(valid date) error: %v", err)
	}
	if got.Year() != 2025 || got.Month() != 1 || got.Day() != 15 {
		t.Errorf("ParseRelativeTime(2025-01-15) = %v, want 2025-01-15", got)
	}
}

// TestParseUpperBoundTime_DateOnlySnapsToEndOfDay pins beads-ci44e: a bare date
// (YYYY-MM-DD) used as a `--X-before` upper bound must snap to END-of-day so the
// bound includes everything that happened DURING that day on real-timestamp
// columns (created_at/updated_at/closed_at). ParseRelativeTime alone parses a
// bare date to MIDNIGHT start-of-day, which as an upper bound excludes every
// intra-day timestamp — the exact bug (a row created 2026-07-20T06:15Z was
// dropped by --created-before 2026-07-20, and an equal-bounds point query was
// always empty). Explicit timestamps must be used verbatim (NOT snapped).
func TestParseUpperBoundTime_DateOnlySnapsToEndOfDay(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	// Bare date -> end of that calendar day.
	got, err := ParseUpperBoundTime("2026-07-20", now)
	if err != nil {
		t.Fatalf("ParseUpperBoundTime(2026-07-20) error: %v", err)
	}
	y, m, d := got.Date()
	if y != 2026 || m != time.July || d != 20 {
		t.Errorf("date = %04d-%02d-%02d, want 2026-07-20", y, m, d)
	}
	if got.Hour() != 23 || got.Minute() != 59 || got.Second() != 59 {
		t.Errorf("upper bound = %v, want end-of-day 23:59:59 (beads-ci44e)", got)
	}
	// An intra-day timestamp on the same day must sit at/under the upper bound
	// (the original bug: it sat ABOVE the midnight bound and was excluded).
	intraDay := time.Date(2026, 7, 20, 6, 15, 27, 0, got.Location())
	if intraDay.After(got) {
		t.Errorf("intra-day %v is After upper bound %v — same-day row would be dropped (beads-ci44e)", intraDay, got)
	}
	// And a start-of-day lower bound for the same date must sit at/under it, so
	// an equal-bounds point query (--after D --before D) is non-empty.
	startOfDay := time.Date(2026, 7, 20, 0, 0, 0, 0, got.Location())
	if startOfDay.After(got) {
		t.Errorf("start-of-day %v After upper bound %v — equal-bounds point query would be empty (beads-ci44e)", startOfDay, got)
	}

	// Explicit RFC3339 timestamp is used AS-IS (not snapped to end-of-day).
	exact, err := ParseUpperBoundTime("2026-07-20T06:15:00Z", now)
	if err != nil {
		t.Fatalf("ParseUpperBoundTime(RFC3339) error: %v", err)
	}
	if exact.Hour() != 6 || exact.Minute() != 15 {
		t.Errorf("explicit timestamp = %v, want 06:15 preserved (only bare dates snap)", exact)
	}

	// IsDateOnly discriminates the two forms the snap keys on.
	if !IsDateOnly("2026-07-20") {
		t.Error("IsDateOnly(2026-07-20) = false, want true")
	}
	if IsDateOnly("2026-07-20T06:15:00Z") {
		t.Error("IsDateOnly(RFC3339) = true, want false")
	}
}
