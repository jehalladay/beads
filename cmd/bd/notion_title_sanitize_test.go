package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/notion"
)

// TestRenderNotionStatus_SanitizesDatabaseTitle_a01ve is the sanitize teeth for
// beads-a01ve (7n9y sink-class slice). `bd notion status` (renderNotionStatus,
// notion.go:406) printed result.Database.Title RAW via fmt.Fprintf. That Title
// is notion.StatusDatabase.Title — JSON-decoded from an EXTERNAL Notion API
// response (a genuinely untrusted remote source), so it can carry OSC/CSI
// terminal-control escapes (OSC 52 clipboard-write, OSC 0/2 window-title-set).
// The fix routes it through displayTitle (ui.SanitizeForTerminal); display-only
// — the --json path goes through writeNotionJSON (notion.go:248/284), never
// this render func, so the JSON contract stays raw.
//
// renderNotionStatus is pure (writes cmd.OutOrStdout()), so this exercises it
// directly with a bytes.Buffer sink.
func TestRenderNotionStatus_SanitizesDatabaseTitle_a01ve(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	rawTitle := "Danger" + csi + osc + "Title"

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	result := &notion.StatusResponse{
		Ready: true,
		Database: &notion.StatusDatabase{
			ID:    "db-evil",
			Title: rawTitle,
		},
	}

	renderNotionStatus(cmd, nil, notionConfig{}, result)

	out := buf.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("bd notion status leaked a raw ESC (\\x1b) — Database.Title not sanitized (beads-a01ve):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("bd notion status leaked a raw BEL (\\x07) — Database.Title not sanitized (beads-a01ve):\n%q", out)
	}
	// Visible title text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "Danger") || !strings.Contains(out, "Title") {
		t.Errorf("bd notion status dropped the visible title text (over-sanitized): %q", out)
	}
}
