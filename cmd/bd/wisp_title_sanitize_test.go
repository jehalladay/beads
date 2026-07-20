package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestRenderWispGCDryRunTitleSanitize_hacdt is the sanitize teeth for
// beads-hacdt (7n9y sink-enum delta). `bd wisp gc --dry-run` printed each
// abandoned wisp as "  <id>: <issue.Title> (last updated: <age>)" via bare
// fmt.Printf (wisp.go:730), bypassing ui.SanitizeForTerminal. A wisp Title
// comes from stored issue data (an untrusted JSONL/markdown/SCM import can
// carry OSC/CSI escapes — OSC 0 window-title / OSC 52 clipboard), so the
// human dry-run preview injected terminal-control sequences. The fix routes
// the title through displayTitle in a testable renderWispGCDryRun helper. The
// --json path (WispGCResult) is unaffected.
func TestRenderWispGCDryRunTitleSanitize_hacdt(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	abandoned := []*types.Issue{
		{ID: "bd-w1", Title: "Danger" + csi + osc + "Wisp"},
	}

	out := renderWispGCDryRun(abandoned)

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("wisp gc dry-run leaked a raw ESC (\\x1b) — title not sanitized (beads-hacdt):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("wisp gc dry-run leaked a raw BEL (\\x07) — title not sanitized (beads-hacdt):\n%q", out)
	}
	// Visible text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "Danger") || !strings.Contains(out, "Wisp") {
		t.Errorf("visible title text did not survive sanitize:\n%q", out)
	}
	if !strings.Contains(out, "bd-w1") {
		t.Errorf("wisp id missing from rendered dry-run:\n%q", out)
	}
}
