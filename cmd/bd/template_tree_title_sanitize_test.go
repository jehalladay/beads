package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPrintTemplateTree_SanitizesTitle_8imcg is the sanitize teeth for
// beads-8imcg (7n9y sink-class slice). printTemplateTreeVisited (the 'bd mol'
// template-structure tree preview) printed the root/child issue Title RAW via
// bare fmt.Printf at template.go:682 (root), :710 (cycle-detected node), and
// :713 (normal child). These are template/proto issues read from the store; a
// template imported from JSONL/markdown/SCM carries its title verbatim, so an
// OSC/CSI escape reached the terminal. The fix routes each through displayTitle
// (ui.SanitizeForTerminal); display-only.
//
// printTemplateTree is pure (takes *TemplateSubgraph, writes stdout), so this
// builds an in-memory subgraph (root + one child) and calls it via captureStdout.
func TestPrintTemplateTree_SanitizesTitle_8imcg(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	rootTitle := "RootDanger" + csi + osc + "Root"
	childTitle := "ChildDanger" + csi + osc + "Child"

	root := &types.Issue{ID: "tmpl-root", Title: rootTitle, IssueType: "epic"}
	child := &types.Issue{ID: "tmpl-child", Title: childTitle, IssueType: "task"}

	subgraph := &TemplateSubgraph{
		Root:   root,
		Issues: []*types.Issue{root, child},
		Dependencies: []*types.Dependency{
			{IssueID: child.ID, DependsOnID: root.ID, Type: types.DepParentChild},
		},
		IssueMap: map[string]*types.Issue{root.ID: root, child.ID: child},
	}

	out := captureStdout(t, func() error {
		printTemplateTree(subgraph, root.ID, 0, true)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("template tree leaked a raw ESC (\\x1b) — title not sanitized (beads-8imcg):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("template tree leaked a raw BEL (\\x07) — title not sanitized (beads-8imcg):\n%q", out)
	}
	// Visible title text must survive sanitize for BOTH root (L682) and child
	// (L713) sinks (escapes stripped, text kept).
	if !strings.Contains(out, "RootDangerRoot") {
		t.Errorf("template tree dropped/garbled root title (L682) (beads-8imcg):\n%q", out)
	}
	if !strings.Contains(out, "ChildDangerChild") {
		t.Errorf("template tree dropped/garbled child title (L713) (beads-8imcg):\n%q", out)
	}
}
