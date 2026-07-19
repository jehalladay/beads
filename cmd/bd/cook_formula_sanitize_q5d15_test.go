package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/formula"
)

// beads-q5d15 (7n9y sink-class): two cmd/bd tree renderers printed step.Title
// RAW via fmt.Printf, bypassing SanitizeForTerminal —
//   - printFormulaSteps (cook.go, `bd cook` tree)
//   - printFormulaStepsTree (formula.go, formula tree)
//
// A step title from an untrusted formula (JSONL/markdown/SCM import) can carry
// OSC/CSI terminal-control escapes; the raw render injects them. Both now route
// step.Title through displayTitle(). Mirrors the landed cp8k6 (mol show) fix.
func TestCookFormulaTreeSanitize_q5d15(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	steps := []*formula.Step{
		{ID: "s1", Title: "Build" + osc, Type: "task"},
		{ID: "s2", Title: "Deploy" + csi + osc, Type: "task"},
	}

	assertClean := func(t *testing.T, label, got string) {
		t.Helper()
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (\\x1b): %q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (\\x07): %q", label, got)
		}
		if !strings.Contains(got, "Build") || !strings.Contains(got, "Deploy") {
			t.Errorf("%s: visible step titles did not survive sanitize: %q", label, got)
		}
	}

	cookOut := captureStdout(t, func() error {
		printFormulaSteps(steps, "")
		return nil
	})
	assertClean(t, "bd cook tree (printFormulaSteps)", cookOut)

	formulaOut := captureStdout(t, func() error {
		printFormulaStepsTree(steps, "")
		return nil
	})
	assertClean(t, "formula tree (printFormulaStepsTree)", formulaOut)
}
