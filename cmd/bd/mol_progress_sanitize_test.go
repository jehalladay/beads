package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-6ecvi (7n9y slice): printMoleculeProgressStats printed the molecule
// title RAW via fmt.Printf (mol_progress.go:143), bypassing
// ui.SanitizeForTerminal — ui.RenderAccent wraps the ID, not the title. A
// molecule title can originate from an untrusted import (JSONL/markdown/SCM)
// carrying OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52
// clipboard), so the human-readable `bd mol progress` view injected control
// sequences onto its header line. The fix routes the title through
// displayTitle. Display-only: the --json path (molecule_title) and stored
// titles are unchanged.

func TestPrintMoleculeProgressStats_sanitizeTitle(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	stats := &types.MoleculeProgressStats{
		MoleculeID:    "mol-1",
		MoleculeTitle: "Alpha" + osc + csi + "Mol",
		Total:         3,
		Completed:     1,
	}

	out := captureStdout(t, func() error {
		printMoleculeProgressStats(stats)
		return nil
	})

	assertNoRawEscapes(t, out, "mol progress header")
	for _, want := range []string{"AlphaMol", "mol-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("mol progress header dropped %q: %q", want, out)
		}
	}
}
