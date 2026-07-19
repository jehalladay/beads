package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-fd (7n9y slice): `bd find-duplicates` printed each pair's IssueA.Title
// and IssueB.Title RAW via fmt.Printf (find_duplicates.go), bypassing the
// ui.SanitizeForTerminal sanitize that human-readable output applies. A title
// can originate from an untrusted import (JSONL/markdown/SCM) carrying OSC/CSI
// terminal-control escapes (OSC 0 window-title / OSC 52 clipboard), so the
// duplicate-pair list injected control sequences onto those lines. The fix
// routes both title sinks through displayTitle. The AI-analysis prompt
// (analyzeWithAI) intentionally keeps raw titles and is NOT a terminal sink.
func TestPrintDuplicatePairs_sanitize(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	pairs := []duplicatePair{
		{
			IssueA:     &types.Issue{ID: "issue-a", Title: "Alpha" + osc + "Title"},
			IssueB:     &types.Issue{ID: "issue-b", Title: "Beta" + csi + osc + "Title"},
			Similarity: 0.9,
			Reason:     "similar tokens",
		},
	}

	out := captureStdout(t, func() error {
		printDuplicatePairs(pairs)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("pair list leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("pair list leaked a raw BEL (\\x07): %q", out)
	}
	// Visible text must survive sanitizing (escapes stripped, chars kept).
	for _, want := range []string{"AlphaTitle", "BetaTitle"} {
		if !strings.Contains(out, want) {
			t.Errorf("pair list dropped visible title text %q: %q", want, out)
		}
	}
	// Structural output must still render: issue IDs.
	for _, want := range []string{"issue-a", "issue-b"} {
		if !strings.Contains(out, want) {
			t.Errorf("pair list dropped structural output %q: %q", want, out)
		}
	}
}
