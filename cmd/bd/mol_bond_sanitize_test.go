package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-hckxx: `bd mol bond --dry-run` printed each operand's issue.Title (and
// the --title custom compound title) RAW via fmt.Printf (mol_bond.go),
// bypassing the ui.SanitizeForTerminal sanitize that human-readable output
// applies. A proto/mol title can originate from an untrusted import (JSONL/
// markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0 window-title /
// OSC 52 clipboard), so the dry-run preview injected control sequences onto
// those lines. The fix routes the operand/custom titles through displayTitle.
// 7n9y sink-class slice.
func TestPrintMolBondDryRun_sanitize_hckxx(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	// Two protos (so the compound-proto branch fires + custom title prints).
	issueA := &types.Issue{
		ID:     "proto-a",
		Title:  "Alpha" + osc + "Title",
		Labels: []string{MoleculeLabel},
	}
	issueB := &types.Issue{
		ID:     "proto-b",
		Title:  "Beta" + csi + osc + "Title",
		Labels: []string{MoleculeLabel},
	}
	customTitle := "Custom" + osc + "Compound"

	out := captureStdout(t, func() error {
		printMolBondDryRun(issueA, issueB, "", "", "proto-a", "proto-b",
			types.BondTypeSequential, customTitle, "", map[string]string{}, false, false)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("dry-run leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("dry-run leaked a raw BEL (\\x07): %q", out)
	}
	// Visible text must survive sanitizing (escapes stripped, chars kept).
	for _, want := range []string{"AlphaTitle", "BetaTitle", "CustomCompound"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run dropped visible title text %q: %q", want, out)
		}
	}
	// Structural output must still render: operand IDs + compound-proto result.
	for _, want := range []string{"proto-a", "proto-b", "compound proto"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run dropped structural output %q: %q", want, out)
		}
	}
}
