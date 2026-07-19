package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDisplayRefGroup_SanitizesTitle_q46dy is the sanitize teeth for
// beads-q46dy (7n9y sink-class slice). displayRefGroup ('bd show <id> --refs'
// output) printed ref.Title RAW: the active-item row via bare ref.Title
// (show_refs.go:153) and the closed-item row via ui.RenderMuted(ref.Title)
// (show_refs.go:136) — RenderMuted only wraps a lipgloss style, it does NOT
// strip control chars, so both bypassed ui.SanitizeForTerminal. A referenced
// issue's stored title can originate from an untrusted import (JSONL/markdown/
// SCM) carrying OSC/CSI escapes (OSC 0 window-title / OSC 52 clipboard). The
// fix routes both through displayTitle; display-only (stored title + --json
// path unchanged).
//
// displayRefGroup is pure (takes a dep type + refs slice, writes stdout), so
// this calls it directly via captureStdout for both a closed and an active ref.
func TestDisplayRefGroup_SanitizesTitle_q46dy(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	refs := []*types.IssueWithDependencyMetadata{
		{Issue: types.Issue{ID: "bd-open", Title: "Active" + csi + osc + "Ref", Priority: 2, Status: types.StatusOpen}},
		{Issue: types.Issue{ID: "bd-done", Title: "Closed" + csi + osc + "Ref", Priority: 1, Status: types.StatusClosed}},
	}

	out := captureStdout(t, func() error {
		displayRefGroup(types.DepBlocks, refs)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("bd show --refs leaked a raw ESC (\\x1b) — title not sanitized (beads-q46dy):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("bd show --refs leaked a raw BEL (\\x07) — title not sanitized (beads-q46dy):\n%q", out)
	}
	// Visible title text must survive sanitize for BOTH the active row (L153)
	// and the closed row (L136).
	if !strings.Contains(out, "ActiveRef") {
		t.Errorf("bd show --refs dropped/garbled active-ref title (L153) (beads-q46dy):\n%q", out)
	}
	if !strings.Contains(out, "ClosedRef") {
		t.Errorf("bd show --refs dropped/garbled closed-ref title (L136) (beads-q46dy):\n%q", out)
	}
}
