package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-dt280 (7n9y slice): displayRefGroup printed ref titles RAW via fmt.Printf
// (the active-row path at show_refs.go:153) and via ui.RenderMuted (the
// closed-row path) — RenderMuted is a lipgloss color wrapper, NOT a sanitizer, so
// it does not strip terminal-control escapes. A ref title can originate from an
// untrusted import (JSONL/markdown/SCM) carrying OSC/CSI escapes (OSC 0
// window-title / OSC 52 clipboard), so `bd show <id> --refs` injected control
// sequences onto its lines. The fix routes each title through displayTitle.
// Display-only: the --json path (outputJSON(allRefs)) and stored titles are
// unchanged.

func TestDisplayRefGroup_sanitizeActiveRow(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	refs := []*types.IssueWithDependencyMetadata{
		{Issue: types.Issue{ID: "bd-1", Title: "Alpha" + osc + csi + "Ref", Priority: 2, Status: types.StatusOpen}},
	}

	out := captureStdout(t, func() error {
		displayRefGroup(types.DepBlocks, refs)
		return nil
	})

	assertNoRawEscapes(t, out, "displayRefGroup active row")
	for _, want := range []string{"AlphaRef", "bd-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("displayRefGroup active row dropped %q: %q", want, out)
		}
	}
}

func TestDisplayRefGroup_sanitizeClosedRow(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"

	refs := []*types.IssueWithDependencyMetadata{
		{Issue: types.Issue{ID: "bd-2", Title: "Beta" + osc + "Ref", Priority: 1, Status: types.StatusClosed}},
	}

	out := captureStdout(t, func() error {
		displayRefGroup(types.DepBlocks, refs)
		return nil
	})

	assertNoRawEscapes(t, out, "displayRefGroup closed row")
	if !strings.Contains(out, "BetaRef") {
		t.Errorf("displayRefGroup closed row dropped visible title: %q", out)
	}
}
