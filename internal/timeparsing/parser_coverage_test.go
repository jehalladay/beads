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
