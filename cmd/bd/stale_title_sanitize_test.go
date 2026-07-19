package main

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestDisplayStaleIssues_SanitizesTitle_hc2pk is the sanitize teeth for
// beads-hc2pk (7n9y sink-class slice). displayStaleIssues ('bd stale' human
// output) printed issue.Title RAW via bare fmt.Printf (stale.go:140),
// bypassing ui.SanitizeForTerminal. A stored title can originate from an
// untrusted import (JSONL/markdown/SCM) carrying OSC/CSI terminal-control
// escapes (OSC 0 window-title / OSC 52 clipboard), so the stale report
// injected the escape onto the terminal. The fix routes it through
// displayTitle; display-only (stored title + the --json path are unchanged).
//
// displayStaleIssues is pure (takes []*types.Issue, writes stdout), so this
// calls it directly via captureStdout.
func TestDisplayStaleIssues_SanitizesTitle_hc2pk(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	issues := []*types.Issue{
		{
			ID:        "bd-a",
			Title:     "Stale" + csi + osc + "Issue",
			Priority:  2,
			Status:    types.StatusOpen,
			UpdatedAt: time.Now().Add(-30 * 24 * time.Hour),
		},
	}

	out := captureStdout(t, func() error {
		displayStaleIssues(issues, 7, false, 50)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("bd stale leaked a raw ESC (\\x1b) — title not sanitized (beads-hc2pk):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("bd stale leaked a raw BEL (\\x07) — title not sanitized (beads-hc2pk):\n%q", out)
	}
	if !strings.Contains(out, "StaleIssue") {
		t.Errorf("bd stale dropped/garbled title (beads-hc2pk):\n%q", out)
	}
}
