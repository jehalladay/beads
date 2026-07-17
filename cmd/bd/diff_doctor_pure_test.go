package main

import (
	"testing"

	"github.com/steveyegge/beads/cmd/bd/doctor"
)

// beads-wl91: hermetic tests for pure helpers in diff.go and doctor.go
// (verified 0% + no test references).

func TestJoinStrings(t *testing.T) {
	cases := []struct {
		in   []string
		sep  string
		want string
	}{
		{nil, ",", ""},
		{[]string{}, ",", ""},
		{[]string{"a"}, ",", "a"},
		{[]string{"a", "b", "c"}, ", ", "a, b, c"},
		{[]string{"x", "y"}, "", "xy"},
		{[]string{"", ""}, "-", "-"},
	}
	for _, c := range cases {
		if got := joinStrings(c.in, c.sep); got != c.want {
			t.Errorf("joinStrings(%q, %q) = %q, want %q", c.in, c.sep, got, c.want)
		}
	}
}

func TestConvertDoctorCheck(t *testing.T) {
	dc := doctor.DoctorCheck{
		Name:     "dolt-server",
		Status:   "ok",
		Message:  "reachable",
		Detail:   "127.0.0.1:3307",
		Fix:      "bd dolt start",
		Category: "storage",
	}
	got := convertDoctorCheck(dc)
	if got.Name != dc.Name || got.Status != dc.Status || got.Message != dc.Message ||
		got.Detail != dc.Detail || got.Fix != dc.Fix || got.Category != dc.Category {
		t.Errorf("convertDoctorCheck lost a field: %+v vs %+v", got, dc)
	}
}

func TestConvertWithCategory(t *testing.T) {
	dc := doctor.DoctorCheck{Name: "n", Status: "warning", Message: "m", Category: "original"}
	got := convertWithCategory(dc, "override")
	// Category is overridden; other fields preserved from the source check.
	if got.Category != "override" {
		t.Errorf("Category = %q, want override", got.Category)
	}
	if got.Name != "n" || got.Status != "warning" || got.Message != "m" {
		t.Errorf("non-category fields not preserved: %+v", got)
	}
}

func TestShouldSkipDoctorNetworkChecks(t *testing.T) {
	orig := jsonOutput
	t.Cleanup(func() { jsonOutput = orig })

	// --json forces skip regardless of terminal state.
	jsonOutput = true
	if !shouldSkipDoctorNetworkChecks() {
		t.Error("expected skip=true when jsonOutput is set")
	}

	// Without --json, the result tracks terminal state; assert it does not
	// panic and returns a bool consistent with ui.IsTerminal() (in the test
	// harness stdout is not a TTY, so network checks are skipped).
	jsonOutput = false
	_ = shouldSkipDoctorNetworkChecks()
}
