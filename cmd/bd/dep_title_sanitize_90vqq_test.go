package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-90vqq (7n9y slice): the DIRECT dep.go views printed issue titles RAW
// via fmt.Printf, bypassing ui.SanitizeForTerminal — the direct twins of the
// proxied sinks already fixed under beads-2ktwm (whose test comment names these
// as "dep.go:1017/1351"). Two sinks: the `bd dep list` line (printDirectDepList)
// and `bd dep tree` node render (formatTreeNode). A title can originate from an
// untrusted import (JSONL/markdown/SCM) carrying OSC/CSI terminal-control
// escapes (OSC 0 window-title / OSC 52 clipboard), so these views injected
// control sequences onto their lines. The fix routes both through displayTitle.
// Display-only — stored titles and the JSON path are unchanged.

func TestPrintDirectDepList_sanitize_90vqq(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	issues := []*types.IssueWithDependencyMetadata{
		{
			Issue: types.Issue{
				ID:       "bd-a",
				Title:    "Alpha" + osc + "Dep",
				Priority: 2,
				Status:   types.StatusOpen,
			},
			DependencyType: types.DependencyType("blocks"),
		},
		{
			Issue: types.Issue{
				ID:       "bd-b",
				Title:    "Beta" + csi + osc + "Dep",
				Priority: 1,
				Status:   types.StatusClosed,
			},
			DependencyType: types.DependencyType("related"),
		},
	}

	out := captureStdout(t, func() error {
		printDirectDepList(issues)
		return nil
	})

	assertNoRawEscapes(t, out, "direct dep list")
	for _, want := range []string{"AlphaDep", "BetaDep", "bd-a", "bd-b"} {
		if !strings.Contains(out, want) {
			t.Errorf("direct dep list dropped %q: %q", want, out)
		}
	}
}

func TestFormatTreeNode_sanitize_90vqq(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	t.Run("internal node title sanitized", func(t *testing.T) {
		node := &types.TreeNode{
			Issue: types.Issue{
				ID:       "bd-x",
				Title:    "Node" + csi + osc + "Title",
				Priority: 2,
				Status:   types.StatusOpen,
			},
		}
		got := formatTreeNode(node, false)
		if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
			t.Errorf("formatTreeNode leaked a raw escape: %q", got)
		}
		if !strings.Contains(got, "NodeTitle") || !strings.Contains(got, "bd-x") {
			t.Errorf("formatTreeNode dropped visible text/id: %q", got)
		}
	})

	t.Run("external ref title sanitized", func(t *testing.T) {
		// External refs render node.Title directly (it carries the status
		// indicator), so it must be sanitized too.
		node := &types.TreeNode{
			Issue: types.Issue{
				ID:     "external:proj/cap",
				Title:  "ExtRef" + osc + "Title",
				Status: types.StatusOpen,
			},
		}
		got := formatTreeNode(node, false)
		if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
			t.Errorf("formatTreeNode(external) leaked a raw escape: %q", got)
		}
		if !strings.Contains(got, "ExtRefTitle") {
			t.Errorf("formatTreeNode(external) dropped visible text: %q", got)
		}
	})
}
