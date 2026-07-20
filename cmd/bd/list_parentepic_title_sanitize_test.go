package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestFormatPrettyIssueWithContext_ParentEpicSanitize is the sanitize teeth for
// beads-cmzco (7n9y sink-class slice missed by aoz1q). formatPrettyIssueWithContext
// (list_format.go, only prod caller ready.go:560) appended the parent-epic title
// via ui.RenderMuted("← "+parentEpic) — RenderMuted is lipgloss styling only, no
// SanitizeForTerminal — so a raw parentEpic (parent.Title, an untrusted
// stored/imported title) leaked OSC/CSI escapes to the terminal in default
// `bd ready` output. The issue's OWN title was already sanitized via
// formatPrettyIssue→displayTitle (aoz1q); this covers the parent-epic suffix.
// Fix routes parentEpic through displayTitle (ui.SanitizeForTerminal); the
// pure func is tested directly.
func TestFormatPrettyIssueWithContext_ParentEpicSanitize(t *testing.T) {
	issue := &types.Issue{
		ID:        "bd-42",
		Title:     "Implement feature",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
	}
	const osc = "\x1b]8;;http://evil\x1b\\LINK\x1b\\"
	const csi = "\x1b[31m"
	epicTitle := "Auth Overhaul" + csi + osc + "RED"

	out := formatPrettyIssueWithContext(issue, epicTitle)

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("beads-cmzco: parent-epic escapes must be stripped, got raw ESC in %q", out)
	}
	// The visible epic text is preserved (sanitize strips escapes, keeps content).
	if !strings.Contains(out, "Auth Overhaul") {
		t.Errorf("expected visible parent-epic text kept, got %q", out)
	}
	if !strings.Contains(out, "RED") {
		t.Errorf("expected trailing visible epic text kept, got %q", out)
	}
	// Sanity: the issue's own title still renders.
	if !strings.Contains(out, "Implement feature") {
		t.Errorf("expected issue title in output, got %q", out)
	}
}
