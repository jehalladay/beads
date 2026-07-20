package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// TestRenderDiff_titleSanitize_mihle is the teeth for beads-mihle: renderDiff
// (shared by direct `bd diff` and the proxied twin diff_proxied_server.go)
// printed issue titles RAW in the Added section (entry.NewValue.Title) and the
// Removed section (ui.RenderMuted(entry.OldValue.Title) — RenderMuted only
// applies lipgloss styling and does NOT strip control escapes). This is the
// i8dsb/7n9y display-sanitize class; `bd diff` was skipped by the sweep exactly
// like `bd history` (beads-f956y). An untrusted title (OSC/CSI escapes from a
// JSONL/markdown/SCM import) reached the terminal verbatim in the ref-diff view.
// Both title sinks now route through ui.SanitizeForTerminal.
func TestRenderDiff_titleSanitize_mihle(t *testing.T) {
	const osc = "\x1b]0;pwned\x07" // OSC 0: set window title
	const csi = "\x1b[31m"         // CSI SGR red

	// renderDiff reads the global jsonOutput; force the human render path.
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	entries := []*storage.DiffEntry{
		{
			IssueID:  "bd-added",
			DiffType: "added",
			NewValue: &types.Issue{ID: "bd-added", Title: "Add" + csi + osc + "Ed", Status: types.StatusOpen},
		},
		{
			IssueID:  "bd-removed",
			DiffType: "removed",
			OldValue: &types.Issue{ID: "bd-removed", Title: "Rem" + csi + osc + "Oved", Status: types.StatusClosed},
		},
	}

	out := captureStdout(t, func() error {
		return renderDiff(entries, "refA", "refB")
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("renderDiff leaked a raw ESC (0x1b) — a diff title was not sanitized:\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("renderDiff leaked a raw BEL (0x07) — a diff title was not sanitized:\n%q", out)
	}
	// The visible title text (escapes stripped) must survive in both sections.
	if !strings.Contains(out, "AddEd") {
		t.Errorf("renderDiff dropped the Added title's visible text; out:\n%q", out)
	}
	if !strings.Contains(out, "RemOved") {
		t.Errorf("renderDiff dropped the Removed title's visible text; out:\n%q", out)
	}
}
