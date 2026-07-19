package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-7n9y: `bd doctor --check=pollution` (human path) printed each
// pollutionResult issue.Title RAW via fmt.Printf (doctor_pollution.go:80/91),
// bypassing the ui.SanitizeForTerminal sanitize that `bd show`/list and the
// create/gate/restore paths (j8li/ihaw) apply. A pollution-check title can
// originate from an untrusted import (JSONL/markdown/SCM) carrying OSC/CSI
// terminal-control escapes (OSC 0 window-title / OSC 52 clipboard), so the
// render injected control sequences onto these lines. The fix routes every
// Title sink through displayTitle. Sink-class tail of j8li/ihaw.
func TestPrintPollutionConfidenceGroup_sanitize_7n9y(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	group := []pollutionResult{
		{
			issue:   &types.Issue{ID: "bd-high", Title: "Test Alpha" + osc},
			score:   0.95,
			reasons: []string{"title matches test prefix"},
		},
		{
			issue:   &types.Issue{ID: "bd-med", Title: "Test" + csi + osc + "Beta"},
			score:   0.80,
			reasons: []string{"created by test actor"},
		},
	}

	out := captureStdout(t, func() error {
		printPollutionConfidenceGroup("High Confidence (score ≥ 0.9):", group)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("pollution render leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("pollution render leaked a raw BEL (\\x07): %q", out)
	}
	// Visible title text must survive sanitize.
	if !strings.Contains(out, "Test Alpha") || !strings.Contains(out, "TestBeta") {
		t.Errorf("pollution render dropped visible title text: %q", out)
	}
	// Structural output (header, IDs, scores, reasons, total) must still render.
	for _, want := range []string{"High Confidence", "bd-high", "bd-med", "0.95", "0.80", "title matches test prefix", "(Total: 2 issues)"} {
		if !strings.Contains(out, want) {
			t.Errorf("pollution render dropped structural output %q: %q", want, out)
		}
	}
}

// TestPrintPollutionConfidenceGroup_empty pins the no-op-on-empty contract so
// an empty tier prints nothing (matches the prior `if len(group) > 0` guard).
func TestPrintPollutionConfidenceGroup_empty(t *testing.T) {
	out := captureStdout(t, func() error {
		printPollutionConfidenceGroup("High Confidence (score ≥ 0.9):", nil)
		return nil
	})
	if out != "" {
		t.Errorf("empty group should print nothing, got %q", out)
	}
}
