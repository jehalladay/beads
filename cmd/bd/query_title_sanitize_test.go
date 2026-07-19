package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestFormatQueryIssue_SanitizesTitle_asjzp is the sanitize teeth for
// beads-asjzp (7n9y sink-class slice). `bd query` prints issue.Title RAW in
// two places:
//   - outputQueryResults longFormat (query.go:315) via bare fmt.Printf
//   - formatQueryIssue compact lines (query.go:352 closed / :361 open) into a
//     strings.Builder
//
// bypassing ui.SanitizeForTerminal. A title from an untrusted import
// (JSONL/markdown/SCM stores it raw) can carry OSC/CSI terminal-control escapes
// (OSC 0 window-title / OSC 52 clipboard), so `bd query` injected control
// sequences into the terminal. The fix routes each Title sink through
// displayTitle (display-only; the STORED title + the --json round-trip path
// stay raw). Same helper the delete/completions/mol_* sink slices use.
//
// formatQueryIssue is a pure formatter (no TTY/DB), so it is tested directly
// for both the closed-line and open-line branches.
func TestFormatQueryIssue_SanitizesTitle_asjzp(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	rawTitle := "Danger" + csi + osc + "Title"

	for _, tc := range []struct {
		name   string
		status types.Status
	}{
		{"open_line", types.StatusOpen},
		{"closed_line", types.StatusClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			formatQueryIssue(&buf, &types.Issue{
				ID:        "bd-1",
				IssueType: "bug",
				Priority:  1,
				Title:     rawTitle,
				Status:    tc.status,
			})
			out := buf.String()
			if strings.ContainsRune(out, '\x1b') {
				t.Errorf("formatQueryIssue leaked a raw ESC from the title (beads-asjzp): %q", out)
			}
			if strings.ContainsRune(out, '\x07') {
				t.Errorf("formatQueryIssue leaked a raw BEL from the title (beads-asjzp): %q", out)
			}
			// Visible text survives sanitize.
			if !strings.Contains(out, "Danger") || !strings.Contains(out, "Title") {
				t.Errorf("formatQueryIssue dropped the visible title text: %q", out)
			}
		})
	}
}

// TestOutputQueryResults_LongFormatSanitizesTitle_asjzp covers the
// outputQueryResults longFormat branch (query.go:315), which prints the title
// via a bare fmt.Printf to stdout.
func TestOutputQueryResults_LongFormatSanitizesTitle_asjzp(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	rawTitle := "Danger" + osc + "Title"

	out := captureStdout(t, func() error {
		outputQueryResults([]*types.Issue{{
			ID:        "bd-1",
			IssueType: "bug",
			Priority:  1,
			Title:     rawTitle,
			Status:    types.StatusOpen,
		}}, "some query", true /*longFormat*/, false, 50)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("outputQueryResults(long) leaked a raw ESC from the title (beads-asjzp): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("outputQueryResults(long) leaked a raw BEL from the title (beads-asjzp): %q", out)
	}
	if !strings.Contains(out, "Danger") || !strings.Contains(out, "Title") {
		t.Errorf("outputQueryResults(long) dropped the visible title text: %q", out)
	}
}
