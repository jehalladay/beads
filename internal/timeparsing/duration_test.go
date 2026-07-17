package timeparsing

import (
	"testing"
	"time"
)

func TestParseCompactDuration(t *testing.T) {
	// Fixed reference time for deterministic tests
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		// Valid positive durations
		{
			name:  "+6h adds 6 hours",
			input: "+6h",
			want:  time.Date(2025, 6, 15, 18, 0, 0, 0, time.UTC),
		},
		{
			name:  "+1d adds 1 day",
			input: "+1d",
			want:  time.Date(2025, 6, 16, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "+2w adds 2 weeks",
			input: "+2w",
			want:  time.Date(2025, 6, 29, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "+3m adds 3 months",
			input: "+3m",
			want:  time.Date(2025, 9, 15, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "+1y adds 1 year",
			input: "+1y",
			want:  time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC),
		},

		// Valid negative durations (past)
		{
			name:  "-1d subtracts 1 day",
			input: "-1d",
			want:  time.Date(2025, 6, 14, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "-2w subtracts 2 weeks",
			input: "-2w",
			want:  time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "-6h subtracts 6 hours",
			input: "-6h",
			want:  time.Date(2025, 6, 15, 6, 0, 0, 0, time.UTC),
		},

		// No sign means positive
		{
			name:  "3m without sign adds 3 months",
			input: "3m",
			want:  time.Date(2025, 9, 15, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "1y without sign adds 1 year",
			input: "1y",
			want:  time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "6h without sign adds 6 hours",
			input: "6h",
			want:  time.Date(2025, 6, 15, 18, 0, 0, 0, time.UTC),
		},

		// Multi-digit amounts
		{
			name:  "+24h adds 24 hours",
			input: "+24h",
			want:  time.Date(2025, 6, 16, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "+365d adds 365 days",
			input: "+365d",
			want:  time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC),
		},

		// Invalid inputs
		{
			name:    "6h+ (sign at end) is invalid",
			input:   "6h+",
			wantErr: true,
		},
		{
			name:    "++1d (double sign) is invalid",
			input:   "++1d",
			wantErr: true,
		},
		{
			name:    "1x (unknown unit) is invalid",
			input:   "1x",
			wantErr: true,
		},
		{
			name:    "empty string is invalid",
			input:   "",
			wantErr: true,
		},
		{
			name:    "just a number is invalid",
			input:   "6",
			wantErr: true,
		},
		{
			name:    "just a unit is invalid",
			input:   "h",
			wantErr: true,
		},
		{
			name:    "spaces are invalid",
			input:   "+ 6h",
			wantErr: true,
		},
		{
			name:    "ISO date is not compact duration",
			input:   "2025-01-15",
			wantErr: true,
		},
		{
			name:    "natural language is not compact duration",
			input:   "tomorrow",
			wantErr: true,
		},
		// beads-85u5: over-cap amounts are rejected instead of overflowing
		// int64 (weeks *7 sign-flip → a PAST date) or producing an absurd date.
		{
			name:    "weeks over cap rejected (would int64-overflow via *7)",
			input:   "9223372036854775807w",
			wantErr: true,
		},
		{
			name:    "years over cap rejected (would be an absurd far-future date)",
			input:   "1500000000000000000y",
			wantErr: true,
		},
		{
			name:    "amount just over cap rejected",
			input:   "1000001w",
			wantErr: true,
		},
		{
			name:  "amount at cap accepted",
			input: "1000000w",
			want:  now.AddDate(0, 0, 1000000*7),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCompactDuration(tt.input, now)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCompactDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("ParseCompactDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsCompactDuration(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"+6h", true},
		{"-1d", true},
		{"+2w", true},
		{"3m", true},
		{"1y", true},
		{"+24h", true},
		{"", false},
		{"tomorrow", false},
		{"2025-01-15", false},
		{"6h+", false},
		{"++1d", false},
		{"1x", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isCompactDuration(tt.input)
			if got != tt.want {
				t.Errorf("isCompactDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseCompactDuration_MonthBoundary tests month arithmetic edge cases.
// Month/year shifts CLAMP to the target month's last valid day rather than
// overflowing forward via Go's AddDate (beads-aysw): Jan 31 + 1 month is
// Feb 28 (2025 non-leap), not March 3. Overflow would silently skew a
// month-relative query threshold on the 29th-31st.
func TestParseCompactDuration_MonthBoundary(t *testing.T) {
	jan31 := time.Date(2025, 1, 31, 12, 0, 0, 0, time.UTC)
	got, err := ParseCompactDuration("+1m", jan31)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2025, 2, 28, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Jan 31 + 1m = %v, want %v (clamp to Feb 28, not AddDate overflow)", got, want)
	}

	// Leap year: Jan 31, 2024 + 1 month clamps to Feb 29.
	jan31Leap := time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC)
	gotLeap, err := ParseCompactDuration("+1m", jan31Leap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantLeap := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
	if !gotLeap.Equal(wantLeap) {
		t.Errorf("Jan 31, 2024 + 1m = %v, want %v (clamp to Feb 29)", gotLeap, wantLeap)
	}

	// Backward month shift on a month-end also clamps: Mar 31 - 1m = Feb 28.
	mar31 := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	gotBack, err := ParseCompactDuration("-1m", mar31)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantBack := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)
	if !gotBack.Equal(wantBack) {
		t.Errorf("Mar 31 - 1m = %v, want %v (clamp to Feb 28, not Mar 3)", gotBack, wantBack)
	}
}

// TestParseCompactDuration_LeapYear tests leap year handling.
func TestParseCompactDuration_LeapYear(t *testing.T) {
	// Feb 28, 2024 (leap year) + 1d = Feb 29
	feb28_2024 := time.Date(2024, 2, 28, 12, 0, 0, 0, time.UTC)
	got, err := ParseCompactDuration("+1d", feb28_2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Feb 28, 2024 + 1d = %v, want %v", got, want)
	}
}

// TestParseCompactDuration_PreservesTimezone tests that local timezone is preserved.
func TestParseCompactDuration_PreservesTimezone(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("timezone America/New_York not available")
	}

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, loc)
	got, err := ParseCompactDuration("+1d", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Location() != loc {
		t.Errorf("timezone not preserved: got %v, want %v", got.Location(), loc)
	}
}
