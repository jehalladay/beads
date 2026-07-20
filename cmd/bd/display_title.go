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

// displayLabels returns a per-element terminal-sanitized COPY of an issue's
// labels for human list/show output (beads-smrvu, the xsmon label-sink axis
// sibling that covers the issue-listing views). Label values arrive from
// untrusted markdown/JSONL/SCM import and validateLabelValue permits ESC/OSC/CSI
// bytes (it rejects only comma/newline/>255), so a poisoned label can inject
// terminal control sequences (OSC 52 clipboard-write, OSC 0/2 window-title) when
// a listing renders them via fmt %v.
//
// Display-only: the returned slice is a fresh copy — the STORED labels slice is
// never mutated (preserving round-trip fidelity), and the JSON output path does
// not route through here.
func displayLabels(labels []string) []string {
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = ui.SanitizeForTerminal(l)
	}
	return out
}
