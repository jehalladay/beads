package main

import (
	"strings"
	"testing"
)

// TestLintTextSanitizesTitle_v2npj is the sanitize teeth for beads-v2npj (7n9y
// sink-class residual). `bd lint` text output printed a stored issue Title RAW:
// the template-warnings loop (lint.go, r.Title) and the structural-inconsistency
// loop (inc.Title). A title is store-read and an untrusted import (JSONL/markdown/
// SCM) carries it verbatim, so an OSC/CSI sequence reached the terminal. The fix
// routes both through displayTitle (ui.SanitizeForTerminal) via the extracted
// pure renderers renderLintTemplateWarnings / renderLintInconsistencies.
// Display-only: the --json path (LintResult.Title / InconsistencyResult.Title)
// stays raw for round-trip fidelity, so this exercises only the text renderers.
const lintOSC_v2npj = "\x1b]0;pwned\x07"
const lintCSI_v2npj = "\x1b[31m"

func assertNoEscapesTitleKept_v2npj(t *testing.T, out, label string) {
	t.Helper()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("%s leaked a raw ESC (\\x1b) — issue.Title not sanitized (beads-v2npj):\n%q", label, out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("%s leaked a raw BEL (\\x07) — issue.Title not sanitized (beads-v2npj):\n%q", label, out)
	}
	if !strings.Contains(out, "Danger") || !strings.Contains(out, "Title") {
		t.Errorf("%s dropped the visible title text (over-sanitized): %q", label, out)
	}
}

func TestLintTemplateWarnings_SanitizesTitle_v2npj(t *testing.T) {
	results := []LintResult{
		{
			ID:      "beads-evil",
			Title:   "Danger" + lintCSI_v2npj + lintOSC_v2npj + "Title",
			Type:    "bug",
			Missing: []string{"Steps to Reproduce"},
		},
	}
	out := captureStdout(t, func() error {
		renderLintTemplateWarnings(results, 1)
		return nil
	})
	assertNoEscapesTitleKept_v2npj(t, out, "bd lint template warnings")
}

func TestLintInconsistencies_SanitizesTitle_v2npj(t *testing.T) {
	inconsistencies := []InconsistencyResult{
		{
			ID:           "beads-evil",
			Title:        "Danger" + lintCSI_v2npj + lintOSC_v2npj + "Title",
			Kind:         "closed_epic_with_open_children",
			OpenChildren: []string{"beads-child1"},
		},
	}
	out := captureStdout(t, func() error {
		renderLintInconsistencies(inconsistencies)
		return nil
	})
	assertNoEscapesTitleKept_v2npj(t, out, "bd lint structural inconsistencies")
}
