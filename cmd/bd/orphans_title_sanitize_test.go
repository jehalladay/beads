package main

import (
	"strings"
	"testing"
)

// TestRenderOrphanListTitleSanitize_rpntv is the sanitize teeth for beads-rpntv
// (7n9y sink-enum delta). `bd orphans` printed each orphan as
// "N. <id>: <orphan.Title>" and, under --details, the raw git
// LatestCommitMessage — both via bare fmt.Printf (orphans.go:84/87), bypassing
// ui.SanitizeForTerminal. An orphan's Title comes from stored issue data (an
// untrusted import can carry OSC/CSI escapes) and the commit message is raw git
// content, so the human list injected terminal-control sequences. The fix
// routes both through displayTitle in a testable renderOrphanList helper. The
// --json path (outputJSON) is unaffected.
func TestRenderOrphanListTitleSanitize_rpntv(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	orphans := []orphanIssueOutput{
		{
			IssueID:             "or-1",
			Title:               "Danger" + csi + osc + "Orphan",
			Status:              "open",
			LatestCommit:        "abc123",
			LatestCommitMessage: "commit" + osc + "msg",
		},
	}

	out := renderOrphanList(orphans, true)

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("orphan list leaked a raw ESC (\\x1b) — title/commit-message not sanitized (beads-rpntv):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("orphan list leaked a raw BEL (\\x07) — title/commit-message not sanitized (beads-rpntv):\n%q", out)
	}
	// Visible text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "Danger") || !strings.Contains(out, "Orphan") {
		t.Errorf("visible title text did not survive sanitize:\n%q", out)
	}
	if !strings.Contains(out, "or-1") {
		t.Errorf("orphan id missing from rendered list:\n%q", out)
	}
	// --details commit message text survives.
	if !strings.Contains(out, "commit") && !strings.Contains(out, "msg") {
		t.Errorf("commit-message text did not survive sanitize:\n%q", out)
	}
}
