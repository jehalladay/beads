package ui

import (
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

// restoreDisabledStyles resets the package-level color/style globals back to the
// plain (NoColor) state after a test mutates them via initColors/initStyles.
// The CI/non-TTY default is exactly the DisableColors() state (init() early-returns
// when ShouldUseColor() is false), so this keeps other tests deterministic.
func restoreDisabledStyles(t *testing.T) {
	t.Helper()
	t.Cleanup(DisableColors)
}

// TestDisableColorsResetsToNoColor verifies DisableColors zeroes every color var
// and empties every style so no ANSI escapes are emitted (the hook-context path).
func TestDisableColorsResetsToNoColor(t *testing.T) {
	restoreDisabledStyles(t)

	// First populate colors/styles with real values so the reset is observable.
	initColors(true)
	initStyles()

	DisableColors()

	// Every exported color var must be NoColor after disabling.
	colors := []struct {
		name string
		c    interface{}
	}{
		{"ColorPass", ColorPass}, {"ColorWarn", ColorWarn}, {"ColorFail", ColorFail},
		{"ColorMuted", ColorMuted}, {"ColorAccent", ColorAccent},
		{"ColorStatusOpen", ColorStatusOpen}, {"ColorStatusInProgress", ColorStatusInProgress},
		{"ColorStatusClosed", ColorStatusClosed}, {"ColorStatusBlocked", ColorStatusBlocked},
		{"ColorStatusPinned", ColorStatusPinned}, {"ColorStatusHooked", ColorStatusHooked},
		{"ColorPriorityP0", ColorPriorityP0}, {"ColorPriorityP1", ColorPriorityP1},
		{"ColorPriorityP2", ColorPriorityP2}, {"ColorPriorityP3", ColorPriorityP3},
		{"ColorPriorityP4", ColorPriorityP4},
		{"ColorTypeBug", ColorTypeBug}, {"ColorTypeFeature", ColorTypeFeature},
		{"ColorTypeTask", ColorTypeTask}, {"ColorTypeEpic", ColorTypeEpic},
		{"ColorTypeChore", ColorTypeChore}, {"ColorID", ColorID},
	}
	for _, cc := range colors {
		if _, ok := cc.c.(lipgloss.NoColor); !ok {
			t.Errorf("%s = %#v after DisableColors, want lipgloss.NoColor{}", cc.name, cc.c)
		}
	}

	// With plain styles, rendering must return the input unchanged (no ANSI).
	if got := PassStyle.Render("plain"); got != "plain" {
		t.Errorf("PassStyle.Render after DisableColors = %q, want unchanged %q", got, "plain")
	}
	if got := RenderStatus("in_progress"); got != "in_progress" {
		t.Errorf("RenderStatus after DisableColors = %q, want unchanged", got)
	}
}

// TestInitColorsAndStylesPopulate exercises initColors + initStyles for both the
// dark and light adaptive branches and asserts styles pick up a non-empty
// foreground (so the styled render differs from the plain input).
func TestInitColorsAndStylesPopulate(t *testing.T) {
	restoreDisabledStyles(t)

	for _, isDark := range []bool{true, false} {
		initColors(isDark)
		initStyles()

		// A colored style must alter its output (ANSI wrapping) vs the raw string.
		if got := PassStyle.Render("x"); got == "x" {
			t.Errorf("isDark=%v: PassStyle.Render did not apply color to %q", isDark, "x")
		}
		// CommandStyle is set inside initColors (needs LightDark); it must render.
		if got := CommandStyle.Render("bd"); got == "" {
			t.Errorf("isDark=%v: CommandStyle.Render returned empty", isDark)
		}
		// P0 is bold+colored; ensure it wraps the label.
		if got := PriorityP0Style.Render("P0"); got == "P0" {
			t.Errorf("isDark=%v: PriorityP0Style did not style P0", isDark)
		}
	}
}

// TestGetStatusStyle covers every branch of GetStatusStyle, including the
// default (open/unknown) plain-style path.
func TestGetStatusStyle(t *testing.T) {
	cases := []struct {
		status string
		want   lipgloss.Style
	}{
		{"in_progress", StatusInProgressStyle},
		{"blocked", StatusBlockedStyle},
		{"closed", StatusClosedStyle},
		{"deferred", MutedStyle},
		{"pinned", StatusPinnedStyle},
		{"hooked", StatusHookedStyle},
		{"open", lipgloss.NewStyle()},
		{"totally-custom", lipgloss.NewStyle()},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			got := GetStatusStyle(tc.status)
			// Compare by rendering a probe string — Style is not directly comparable.
			const probe = "▓probe▓"
			if got.Render(probe) != tc.want.Render(probe) {
				t.Errorf("GetStatusStyle(%q) rendered differently than expected", tc.status)
			}
		})
	}
}

// TestRenderPriorityCompactAllLevels covers every priority branch of
// RenderPriorityCompact including the default (out-of-range) fall-through.
func TestRenderPriorityCompactAllLevels(t *testing.T) {
	cases := []struct {
		priority int
		want     string
	}{
		{0, PriorityP0Style.Render("P0")},
		{1, PriorityP1Style.Render("P1")},
		{2, PriorityP2Style.Render("P2")},
		{3, PriorityP3Style.Render("P3")},
		{4, PriorityP4Style.Render("P4")},
		{7, "P7"}, // default: plain label, no styling
	}
	for _, tc := range cases {
		if got := RenderPriorityCompact(tc.priority); got != tc.want {
			t.Errorf("RenderPriorityCompact(%d) = %q, want %q", tc.priority, got, tc.want)
		}
	}
}
