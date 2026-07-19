package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPrintProxiedThread_SanitizesTitle_u88a3 is the sanitize teeth for
// beads-u88a3 (7n9y sink-class slice, proxied twin of beads-s3qhv). The
// proxied 'bd show <id> --thread' view (runShowProxiedThread) printed the
// thread header title (rootMsg.Title, show_proxied_server.go:404) and each
// message's Subject (msg.Title, :424) RAW via bare fmt.Printf, bypassing
// ui.SanitizeForTerminal. A message Subject is settable by other actors (e.g.
// via --stdin) and a title imported from JSONL/markdown/SCM carries its value
// verbatim, so an OSC/CSI escape (OSC 0 window-title / OSC 52 clipboard)
// reached the terminal. The fix extracts the display loop into the pure
// printProxiedThread helper and routes both titles through displayTitle;
// display-only (stored title + JSON path unchanged).
func TestPrintProxiedThread_SanitizesTitle_u88a3(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	root := &types.Issue{ID: "msg-root", Title: "RootSubj" + csi + osc + "Root", IssueType: "message"}
	reply := &types.Issue{ID: "msg-reply", Title: "ReplySubj" + osc + "Reply", IssueType: "message"}

	threadMessages := []*types.Issue{root, reply}
	repliesTo := map[string]string{reply.ID: root.ID}

	out := captureStdout(t, func() error {
		printProxiedThread(threadMessages, repliesTo, root)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("proxied thread leaked a raw ESC (\\x1b) — title not sanitized (beads-u88a3):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("proxied thread leaked a raw BEL (\\x07) — title not sanitized (beads-u88a3):\n%q", out)
	}
	// Visible title text must survive sanitize for BOTH the header (L404,
	// rootMsg.Title) and per-message Subject (L424, msg.Title) sinks.
	if !strings.Contains(out, "RootSubjRoot") {
		t.Errorf("proxied thread dropped/garbled header title (L404) (beads-u88a3):\n%q", out)
	}
	if !strings.Contains(out, "ReplySubjReply") {
		t.Errorf("proxied thread dropped/garbled reply Subject (L424) (beads-u88a3):\n%q", out)
	}
}
