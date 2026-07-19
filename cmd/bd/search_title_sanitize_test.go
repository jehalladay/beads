package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestOutputSearchResults_SanitizesTitle_c9u5w is the sanitize teeth for
// beads-c9u5w (7n9y sink-class slice). outputSearchResults ('bd search' human
// output) printed issue.Title RAW via bare fmt.Printf in BOTH the long format
// (search.go:462) and the compact format (search.go:485), bypassing
// ui.SanitizeForTerminal. A stored title can originate from an untrusted
// import (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard), so a search that surfaced it injected the
// escape onto the terminal. The fix routes both through displayTitle;
// display-only (stored title + the --json path are unchanged).
func TestOutputSearchResults_SanitizesTitle_c9u5w(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	issues := []*types.Issue{
		{ID: "bd-a", Title: "Alpha" + csi + osc + "Hit", Priority: 2, IssueType: "bug", Status: types.StatusOpen},
	}

	for _, tc := range []struct {
		name string
		long bool
	}{
		{"long_format", true},
		{"compact_format", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() error {
				outputSearchResults(issues, "alpha", tc.long, len(issues))
				return nil
			})
			if strings.ContainsRune(out, '\x1b') {
				t.Errorf("search %s leaked a raw ESC (\\x1b) — title not sanitized (beads-c9u5w):\n%q", tc.name, out)
			}
			if strings.ContainsRune(out, '\x07') {
				t.Errorf("search %s leaked a raw BEL (\\x07) — title not sanitized (beads-c9u5w):\n%q", tc.name, out)
			}
			if !strings.Contains(out, "AlphaHit") {
				t.Errorf("search %s dropped/garbled title (beads-c9u5w):\n%q", tc.name, out)
			}
		})
	}
}
