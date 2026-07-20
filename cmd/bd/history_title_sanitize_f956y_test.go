package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// TestFormatHistoryIssueLine_sanitize_f956y is the teeth for beads-f956y: the
// `bd history` views (direct history.go and its proxied twin
// history_proxied_server.go) printed entry.Issue.Title RAW — the one title
// view the i8dsb/7n9y display-sanitize class missed. An issue title can carry
// OSC/CSI terminal-control escapes from an untrusted import (JSONL/markdown/SCM),
// so the Dolt commit-history view injected control sequences on display. Both
// sinks now route through the shared formatHistoryIssueLine helper, which
// sanitizes the title via ui.SanitizeForTerminal before the terminal print.
func TestFormatHistoryIssueLine_sanitize_f956y(t *testing.T) {
	const osc = "\x1b]0;pwned\x07" // OSC 0: set window title
	const csi = "\x1b[31m"         // CSI: SGR red

	entry := &storage.HistoryEntry{
		Issue: &types.Issue{
			ID:       "bd-abc",
			Title:    "Alpha" + csi + osc + "Omega",
			Priority: 2,
			Status:   types.StatusOpen,
		},
	}

	got := formatHistoryIssueLine(entry)

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("formatHistoryIssueLine leaked a raw ESC (0x1b) — title not sanitized:\n%q", got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Errorf("formatHistoryIssueLine leaked a raw BEL (0x07) — title not sanitized:\n%q", got)
	}
	// The visible title text must survive (only the escapes are stripped).
	if !strings.Contains(got, "AlphaOmega") {
		t.Errorf("formatHistoryIssueLine dropped visible title text; got:\n%q", got)
	}
	// The ID and priority still render.
	if !strings.Contains(got, "bd-abc") {
		t.Errorf("formatHistoryIssueLine dropped the issue ID; got:\n%q", got)
	}
}

// TestFormatHistoryIssueLine_cleanTitleUnchanged guards that a benign title is
// rendered verbatim (the sanitizer must not mangle ordinary text).
func TestFormatHistoryIssueLine_cleanTitleUnchanged(t *testing.T) {
	entry := &storage.HistoryEntry{
		Issue: &types.Issue{
			ID:       "bd-xyz",
			Title:    "A normal title with spaces & punctuation!",
			Priority: 0,
			Status:   types.StatusClosed,
		},
	}
	got := formatHistoryIssueLine(entry)
	if !strings.Contains(got, "A normal title with spaces & punctuation!") {
		t.Errorf("formatHistoryIssueLine altered a clean title; got:\n%q", got)
	}
}
