//go:build cgo

package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-ihaw: the create dry-run preview and the restore display print
// issue.Title / issue.Description RAW via fmt.Printf, bypassing the
// ui.SanitizeForTerminal / RenderMarkdown sanitize that `bd show` applies.
// A Description/Title carrying OSC/CSI escapes (from an untrusted
// JSONL/markdown/SCM/backup import — Description has no length cap) injects
// terminal control sequences onto these lines. Sibling of j8li (title sinks).
func TestDescriptionSanitize_ihaw(t *testing.T) {
	// OSC window-title injection + CSI color + raw BEL, plus visible text.
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	title := "My Title" + osc
	desc := "Line one" + csi + osc + "more"

	assertClean := func(t *testing.T, label, got string) {
		t.Helper()
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (\\x1b): %q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (\\x07): %q", label, got)
		}
		if !strings.Contains(got, "My Title") || !strings.Contains(got, "Line one") {
			t.Errorf("%s dropped visible text: %q", label, got)
		}
	}

	mk := func() *types.Issue {
		return &types.Issue{
			ID:          "bd-abc",
			Title:       title,
			Description: desc,
			// restore also echoes Design/AcceptanceCriteria/Notes — all from the
			// same untrusted backup JSONL, so all must be sanitized.
			Design:             "design" + osc,
			AcceptanceCriteria: "ac" + osc,
			Notes:              "notes" + osc,
			Priority:           2,
			IssueType:          types.TypeBug,
			Status:             types.StatusOpen,
		}
	}

	t.Run("renderCreateDryRunPreview", func(t *testing.T) {
		out := captureStdout(t, func() error {
			renderCreateDryRunPreview(mk(), nil, nil)
			return nil
		})
		assertClean(t, "renderCreateDryRunPreview", out)
	})

	t.Run("displayRestoredIssue", func(t *testing.T) {
		out := captureStdout(t, func() error {
			displayRestoredIssue(mk(), "backup.jsonl")
			return nil
		})
		assertClean(t, "displayRestoredIssue", out)
	})
}
