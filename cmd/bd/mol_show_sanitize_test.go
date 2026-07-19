package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-cp8k6: `bd mol show --tree` (printMoleculeTreeWithParallelVisited)
// printed subgraph.Root.Title and child.Title (including the cycle-detected
// branch) RAW via fmt.Printf, bypassing the ui.SanitizeForTerminal sanitize
// applied elsewhere. A molecule/step title can originate from an untrusted
// import (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard), so the tree render injected control
// sequences onto the printed lines. The fix routes every Title sink through
// displayTitle. 7n9y sink-class slice.
func TestPrintMoleculeTree_sanitize_cp8k6(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	root := &types.Issue{ID: "mol-root", Title: "Root Title" + osc, Status: types.StatusOpen}
	child := &types.Issue{ID: "mol-child", Title: "Child" + csi + osc + "Title", Status: types.StatusOpen}

	subgraph := &MoleculeSubgraph{
		Root:   root,
		Issues: []*types.Issue{root, child},
		Dependencies: []*types.Dependency{
			{IssueID: "mol-child", DependsOnID: "mol-root", Type: types.DepParentChild},
		},
		IssueMap: map[string]*types.Issue{"mol-root": root, "mol-child": child},
	}
	// Empty Steps map → getParallelAnnotation(nil) returns "" (no annotation),
	// keeping the fixture minimal while still exercising both sinks.
	analysis := &ParallelAnalysis{
		MoleculeID: "mol-root",
		Steps:      map[string]*ParallelInfo{},
	}

	assertClean := func(t *testing.T, label, got string) {
		t.Helper()
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (\\x1b): %q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (\\x07): %q", label, got)
		}
		// Visible text must survive sanitizing.
		if !strings.Contains(got, "Root Title") || !strings.Contains(got, "ChildTitle") {
			t.Errorf("%s dropped visible title text: %q", label, got)
		}
	}

	t.Run("root + normal child", func(t *testing.T) {
		out := captureStdout(t, func() error {
			printMoleculeTreeWithParallel(subgraph, analysis, "mol-root", 0, true)
			return nil
		})
		assertClean(t, "printMoleculeTreeWithParallel", out)
		// Tree structure must still render.
		if !strings.Contains(out, "└──") {
			t.Errorf("dropped tree connector: %q", out)
		}
	})

	t.Run("cycle-detected branch", func(t *testing.T) {
		// A self-referential parent-child edge forces the cycle branch (line
		// 519) to fire on the second visit of mol-child.
		cyclic := &MoleculeSubgraph{
			Root:   root,
			Issues: []*types.Issue{root, child},
			Dependencies: []*types.Dependency{
				{IssueID: "mol-child", DependsOnID: "mol-root", Type: types.DepParentChild},
				{IssueID: "mol-child", DependsOnID: "mol-child", Type: types.DepParentChild},
			},
			IssueMap: map[string]*types.Issue{"mol-root": root, "mol-child": child},
		}
		out := captureStdout(t, func() error {
			printMoleculeTreeWithParallel(cyclic, analysis, "mol-root", 0, true)
			return nil
		})
		assertClean(t, "cycle branch", out)
		if !strings.Contains(out, "cycle detected") {
			t.Errorf("cycle branch did not fire: %q", out)
		}
	})
}
