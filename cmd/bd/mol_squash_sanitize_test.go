package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-uhiqz: `bd mol squash --dry-run` printed subgraph.Root.Title and each
// wisp child's issue.Title RAW via fmt.Printf (mol_squash.go), bypassing the
// ui.SanitizeForTerminal sanitize that human-readable output applies. A
// molecule/step title can originate from an untrusted import (JSONL/markdown/
// SCM) carrying OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52
// clipboard), so the dry-run preview injected control sequences onto those
// lines. The fix routes both sinks through displayTitle. 7n9y sink-class slice.
//
// This exercises the runMolSquash --dry-run preview path via a subprocess-free
// unit by re-rendering the same two Printf lines is not possible (they are
// inline in RunE), so we drive the command directly with a fixture store.
func TestMolSquashDryRun_sanitize_uhiqz(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	root := &types.Issue{
		ID:     "mol-root",
		Title:  "Root Title" + osc,
		Status: types.StatusInProgress,
	}
	// An ephemeral child so wispChildren is non-empty and the child sink fires.
	child := &types.Issue{
		ID:        "mol-child",
		Title:     "Child" + csi + osc + "Title",
		Status:    types.StatusClosed,
		Ephemeral: true,
	}

	out := captureStdout(t, func() error {
		printMolSquashDryRunPreview(root, []*types.Issue{child}, "mol-root", false)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("dry-run leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("dry-run leaked a raw BEL (\\x07): %q", out)
	}
	// Visible text must survive sanitizing (escapes stripped, chars kept).
	for _, want := range []string{"Root Title", "ChildTitle"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run dropped visible title text %q: %q", want, out)
		}
	}
	// Structural output must still render: IDs + child status.
	for _, want := range []string{"mol-child", "closed"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run dropped structural output %q: %q", want, out)
		}
	}
}
