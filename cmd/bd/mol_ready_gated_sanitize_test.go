package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-khniz (7n9y slice): `bd mol ready --gated` printed each molecule's
// MoleculeTitle and its ReadyStep.Title RAW via fmt.Printf
// (mol_ready_gated.go), bypassing the ui.SanitizeForTerminal sanitize that
// human-readable output applies. A title can originate from an untrusted import
// (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard), so the gate-ready list injected control
// sequences onto those lines. The fix routes both title sinks through
// displayTitle.
func TestPrintGatedReadyMolecules_sanitize(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	molecules := []*GatedMolecule{
		{
			MoleculeID:    "mol-1",
			MoleculeTitle: "Alpha" + osc + "Title",
			ReadyStep: &types.Issue{
				ID:    "step-1",
				Title: "Beta" + csi + osc + "Step",
			},
		},
	}

	out := captureStdout(t, func() error {
		printGatedReadyMolecules(molecules)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("gate-ready list leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("gate-ready list leaked a raw BEL (\\x07): %q", out)
	}
	// Visible text must survive sanitizing (escapes stripped, chars kept).
	for _, want := range []string{"AlphaTitle", "BetaStep"} {
		if !strings.Contains(out, want) {
			t.Errorf("gate-ready list dropped visible title text %q: %q", want, out)
		}
	}
	// Structural output must still render: molecule + step IDs.
	for _, want := range []string{"mol-1", "step-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("gate-ready list dropped structural output %q: %q", want, out)
		}
	}
}
