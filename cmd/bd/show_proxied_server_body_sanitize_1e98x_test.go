package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPrintProxiedThread_SanitizesBody_1e98x is the sanitize teeth for
// beads-1e98x (i8dsb/7n9y sink-class slice, body-text sibling of the u88a3
// Subject fix). The proxied 'bd show <id> --thread' view (printProxiedThread,
// show_proxied_server.go) printed each message's BODY (msg.Description) RAW —
// strings.Split on "\n" then bare fmt.Printf per line — bypassing
// ui.SanitizeForTerminal. A message body is settable by other actors (e.g. via
// 'gt mail send --stdin') and a body imported from JSONL carries its value
// verbatim, so an OSC/CSI escape (OSC 0 window-title / OSC 52 clipboard) reached
// the terminal. This is distinct from beads-u88a3 (Subject) and beads-jxi3d
// (From/To identity) — the same view's body text was the uncovered laggard,
// while plain 'bd show' already OSC-strips the same field via RenderMarkdown.
// The fix sanitizes per line; display-only (stored value + JSON path unchanged).
func TestPrintProxiedThread_SanitizesBody_1e98x(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	// Multi-line body: the escape spans the split so per-line sanitize is required.
	root := &types.Issue{
		ID: "msg-root", Title: "Subj", IssueType: "message",
		Description: "line-one" + csi + osc + "line-two\nline-three" + osc + "END",
	}

	threadMessages := []*types.Issue{root}
	repliesTo := map[string]string{}

	out := captureStdout(t, func() error {
		printProxiedThread(threadMessages, repliesTo, root)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("proxied thread leaked a raw ESC (\\x1b) — body not sanitized (beads-1e98x):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("proxied thread leaked a raw BEL (\\x07) — body not sanitized (beads-1e98x):\n%q", out)
	}
	// Visible body text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "line-oneline-two") {
		t.Errorf("proxied thread dropped/garbled body text (beads-1e98x):\n%q", out)
	}
	if !strings.Contains(out, "line-threeEND") {
		t.Errorf("proxied thread dropped/garbled second body line (beads-1e98x):\n%q", out)
	}
}
