package main

import (
	"strings"
	"testing"
)

// beads-7n9y: shell-completion for issue IDs formatted "ID\tTitle" and printed
// issue.Title RAW (completions.go:56), bypassing the ui.SanitizeForTerminal
// sanitize that bd show/list apply. A title can originate from an untrusted
// import (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard), so the completion menu injected control
// sequences on <TAB>. formatIssueCompletion now routes Title through
// displayTitle. Completion MATCHING is on the ID (before the tab), so the ID
// must survive verbatim while the description is sanitized. Sink-class tail of
// j8li/ihaw.
func TestFormatIssueCompletion_sanitize_7n9y(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	got := formatIssueCompletion("beads-abc", "Danger"+csi+osc+"Title")

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("completion leaked a raw ESC (\\x1b): %q", got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Errorf("completion leaked a raw BEL (\\x07): %q", got)
	}
	// The ID (the actual completion value, before the tab) must be verbatim.
	if !strings.HasPrefix(got, "beads-abc\t") {
		t.Errorf("completion ID must be the verbatim value before the tab, got %q", got)
	}
	// Visible description text must survive sanitize.
	if !strings.Contains(got, "DangerTitle") {
		t.Errorf("completion dropped visible title text: %q", got)
	}
}

// TestFormatIssueCompletion_clean pins the no-op behavior on a clean title: the
// exact "ID\tTitle" shape must be preserved so completion output is unchanged
// for the common case.
func TestFormatIssueCompletion_clean(t *testing.T) {
	if got, want := formatIssueCompletion("beads-abc", "Plain Title"), "beads-abc\tPlain Title"; got != want {
		t.Errorf("clean title should be unchanged: got %q, want %q", got, want)
	}
}
