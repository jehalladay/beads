package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// beads-1y75t (7n9y sink class): two title sinks printed issue.Title RAW,
// bypassing ui.SanitizeForTerminal:
//   - cmd/bd/compact.go candidate tables (dry-run tier / dry candidates /
//     candidates list) — truncated to 40 cols then printed raw.
//   - cmd/bd/delete_proxied_server.go renderDeletePreview — the PROXIED twin of
//     delete.go, which already routes titles through displayTitle.
// A title can originate from an untrusted import (JSONL/markdown/SCM) carrying
// OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52 clipboard), so
// these views injected control sequences. The fix routes both through
// displayTitle (compactDisplayTitle for the truncating compact tables).
// Display-only — stored titles and the JSON path are unchanged.

func TestCompactDisplayTitle_sanitize_1y75t(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	t.Run("strips escapes", func(t *testing.T) {
		got := compactDisplayTitle("Alpha" + csi + osc + "Compact")
		if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
			t.Errorf("compactDisplayTitle leaked a raw escape: %q", got)
		}
		if !strings.Contains(got, "AlphaCompact") {
			t.Errorf("compactDisplayTitle dropped visible text: %q", got)
		}
	})

	t.Run("truncates visible text after sanitizing", func(t *testing.T) {
		// A long escape-laden title must still fit the 40-col column AND carry
		// no raw escapes: sanitize happens before the length check so an escape
		// can never be split across the truncation boundary.
		long := osc + strings.Repeat("x", 80)
		got := compactDisplayTitle(long)
		if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
			t.Errorf("truncated title leaked a raw escape: %q", got)
		}
		if len(got) > 40 {
			t.Errorf("truncated title exceeds 40 cols: len=%d %q", len(got), got)
		}
	})
}

func TestRenderDeletePreview_sanitize_1y75t(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	in := &deleteInput{ids: []string{"bd-a"}}
	preview := domain.DeletePreview{
		Issues: map[string]*types.Issue{
			"bd-a": {ID: "bd-a", Title: "Delete" + osc + "Me"},
		},
		ConnectedIssues: map[string]*types.Issue{
			"bd-c": {ID: "bd-c", Title: "Conn" + csi + osc + "Ref"},
		},
	}
	res := domain.DeleteIssuesResult{DeletedCount: 1}

	out := captureStdout(t, func() error {
		renderDeletePreview(in, preview, res)
		return nil
	})

	assertNoRawEscapes(t, out, "delete preview (proxied)")
	for _, want := range []string{"DeleteMe", "ConnRef", "bd-a", "bd-c"} {
		if !strings.Contains(out, want) {
			t.Errorf("delete preview dropped %q: %q", want, out)
		}
	}
}
