package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-mc7q: `bd mol current` (printMoleculeProgress) and `bd mol continue`
// (PrintContinueResult) printed the molecule title, each step's issue.Title, and
// the next-step title RAW via fmt.Printf (mol_current.go), bypassing the
// ui.SanitizeForTerminal sanitize that human-readable output applies. A
// molecule/step title can originate from an untrusted import (JSONL/markdown/
// SCM) carrying OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52
// clipboard), so the progress views injected control sequences onto those lines.
// The fix routes each display sink through displayTitle. 7n9y sink-class slice.
func TestPrintMoleculeProgress_sanitize_mc7q(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	mol := &MoleculeProgress{
		MoleculeID:    "mol-root",
		MoleculeTitle: "Mol" + osc + "Title",
		// beads-qcboc: mol.Assignee = the root issue's Assignee (untrusted import),
		// printed via "Assigned to: %s" — must also be sanitized at the print site.
		Assignee: "assignee" + csi + osc + "Name",
		Steps: []*StepStatus{
			{
				Issue:  &types.Issue{ID: "step-1", Title: "Step" + csi + osc + "One"},
				Status: "ready",
			},
		},
		NextStep:  &types.Issue{ID: "step-1", Title: "Next" + osc + "Step"},
		Completed: 0,
		Total:     1,
	}

	out := captureStdout(t, func() error {
		printMoleculeProgress(mol)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("progress leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("progress leaked a raw BEL (\\x07): %q", out)
	}
	// Visible text must survive sanitizing (escapes stripped, chars kept).
	for _, want := range []string{"MolTitle", "StepOne", "NextStep", "assigneeName"} {
		if !strings.Contains(out, want) {
			t.Errorf("progress dropped visible title text %q: %q", want, out)
		}
	}
	// Structural output must still render: molecule + step IDs.
	for _, want := range []string{"mol-root", "step-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("progress dropped structural output %q: %q", want, out)
		}
	}
}

func TestPrintLargeMoleculeSummary_sanitize_mc7q(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"

	stats := &types.MoleculeProgressStats{
		MoleculeID:    "mol-big",
		MoleculeTitle: "Large" + osc + "Molecule",
		Total:         500,
		Completed:     10,
	}

	out := captureStdout(t, func() error {
		printLargeMoleculeSummary(stats)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("large-summary leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("large-summary leaked a raw BEL (\\x07): %q", out)
	}
	if !strings.Contains(out, "LargeMolecule") {
		t.Errorf("large-summary dropped visible title text %q: %q", "LargeMolecule", out)
	}
	if !strings.Contains(out, "mol-big") {
		t.Errorf("large-summary dropped structural output %q: %q", "mol-big", out)
	}
}

func TestPrintContinueResult_sanitize_mc7q(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	result := &ContinueResult{
		NextStep:   &types.Issue{ID: "step-2", Title: "Continue" + csi + osc + "Step"},
		MoleculeID: "mol-root",
	}

	out := captureStdout(t, func() error {
		PrintContinueResult(result)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("continue leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("continue leaked a raw BEL (\\x07): %q", out)
	}
	if !strings.Contains(out, "ContinueStep") {
		t.Errorf("continue dropped visible title text %q: %q", "ContinueStep", out)
	}
	if !strings.Contains(out, "step-2") {
		t.Errorf("continue dropped structural output %q: %q", "step-2", out)
	}
}
