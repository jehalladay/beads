package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDisplayTitle_StripsEscapes is the unit teeth for beads-j8li: displayTitle
// must strip terminal-injection escapes (OSC 52 clipboard, OSC 0 title, CSI)
// from a title that may have arrived raw from an untrusted SCM/JSONL/markdown
// import, while preserving the visible text.
func TestDisplayTitle_StripsEscapes(t *testing.T) {
	cases := map[string]string{
		"osc52_clipboard": "safe\x1b]52;c;ZXZpbA==\atail",
		"osc0_title":      "safe\x1b]0;pwned\atail",
		"csi":             "safe\x1b[31mred\x1b[0mtail",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := displayTitle(in)
			if strings.ContainsRune(got, '\x1b') {
				t.Errorf("expected escapes stripped, got %q", got)
			}
			if !strings.Contains(got, "safe") || !strings.Contains(got, "tail") {
				t.Errorf("expected visible text preserved, got %q", got)
			}
		})
	}
}

// TestFormatShortIssue_SanitizesTitle is the end-to-end teeth: a formatter that
// prints a title to the terminal must not leak a raw escape from the stored
// title (beads-j8li). formatShortIssue is a pure formatter (no TTY needed).
func TestFormatShortIssue_SanitizesTitle(t *testing.T) {
	out := formatShortIssue(&types.Issue{
		ID:        "bd-1",
		IssueType: "bug",
		Priority:  1,
		Title:     "boom\x1b]52;c;ZXZpbA==\atail",
		Status:    types.StatusOpen,
	})
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("formatShortIssue leaked a raw escape from the title: %q", out)
	}
	if strings.Contains(out, "]52;") {
		t.Errorf("formatShortIssue leaked the OSC 52 marker: %q", out)
	}
	// Visible text still present.
	if !strings.Contains(out, "boom") || !strings.Contains(out, "tail") {
		t.Errorf("expected visible title text preserved, got %q", out)
	}
}
