package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPrintProxiedMarkdownCreated_SanitizesTitle_jhqu4 is the sanitize teeth
// for beads-jhqu4 (7n9y sink-class slice). The proxied
// 'bd create --from-markdown' summary (runCreateProxiedMarkdown) printed each
// created issue's Title RAW via bare fmt.Printf (create_proxied_server.go:362),
// bypassing ui.SanitizeForTerminal. These titles come directly from the
// imported markdown file (untrusted external source), so an OSC/CSI escape
// (OSC 0 window-title / OSC 52 clipboard) in a heading reached the terminal.
// The fix extracts the display loop into the pure printProxiedMarkdownCreated
// helper and routes the title through displayTitle; display-only (stored
// titles + the JSON path are unchanged).
func TestPrintProxiedMarkdownCreated_SanitizesTitle_jhqu4(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	issues := []*types.Issue{
		{ID: "bd-a", Title: "Alpha" + csi + osc + "Task", Priority: 2, IssueType: "task"},
		{ID: "bd-b", Title: "Beta" + osc + "Epic", Priority: 1, IssueType: "epic"},
	}

	out := captureStdout(t, func() error {
		printProxiedMarkdownCreated(issues, "plan.md")
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("proxied markdown-create leaked a raw ESC (\\x1b) — title not sanitized (beads-jhqu4):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("proxied markdown-create leaked a raw BEL (\\x07) — title not sanitized (beads-jhqu4):\n%q", out)
	}
	for _, want := range []string{"AlphaTask", "BetaEpic", "bd-a", "bd-b"} {
		if !strings.Contains(out, want) {
			t.Errorf("proxied markdown-create dropped/garbled %q (beads-jhqu4):\n%q", want, out)
		}
	}
}
