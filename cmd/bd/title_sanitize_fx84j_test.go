package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-fx84j (final 7n9y sink-class slice): several cmd/bd renderers printed
// issue/node Title RAW via fmt.Printf/Sprintf, bypassing SanitizeForTerminal —
// human list (human.go), markdown dry-run/created (markdown.go), and dep
// cycle/mermaid-tree (dep.go). A title from an untrusted import can carry
// OSC/CSI control escapes; the raw render injects them. All now route Title
// through displayTitle(). This test exercises two representative sinks
// (printHumanList + outputMermaidTree) that are directly callable.
func TestTitleSanitize_fx84j(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	assertClean := func(t *testing.T, label, got string) {
		t.Helper()
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (\\x1b): %q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (\\x07): %q", label, got)
		}
		if !strings.Contains(got, "Fix") {
			t.Errorf("%s: visible title text did not survive sanitize: %q", label, got)
		}
	}

	// bd human list
	humanIssues := []*types.Issue{
		{ID: "h-1", Title: "Fix login" + osc, Status: types.StatusOpen},
	}
	humanOut := captureStdout(t, func() error {
		printHumanList(humanIssues)
		return nil
	})
	assertClean(t, "printHumanList", humanOut)

	// bd dep tree --mermaid
	nodes := []*types.TreeNode{
		{Issue: types.Issue{ID: "d-1", Title: "Fix root" + csi + osc, Status: types.StatusOpen}},
	}
	mermaidOut := captureStdout(t, func() error {
		outputMermaidTree(nodes, "d-1")
		return nil
	})
	assertClean(t, "outputMermaidTree", mermaidOut)
}
