package main

import "github.com/steveyegge/beads/internal/ui"

// displayTitle sanitizes an issue/dependency title for terminal display
// (beads-j8li). A title can originate from an untrusted external source —
// SCM/JSONL/markdown import stores the raw title without sanitizing — and is
// printed on human-readable list/show output. ui.SanitizeForTerminal strips
// CSI/OSC/C0/C1 escapes (notably OSC 52 clipboard-write and OSC 0/2
// window-title-set), so a malicious imported title cannot inject terminal
// control sequences when rendered.
//
// Display-only: the STORED title is untouched (preserving SCM round-trip
// fidelity), and the JSON output path (which must stay raw for round-trip)
// does not route through here — only the human list/show formatters do.
func displayTitle(title string) string {
	return ui.SanitizeForTerminal(title)
}
