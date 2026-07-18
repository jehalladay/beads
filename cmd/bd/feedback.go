package main

import (
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// formatFeedbackID returns "id — title" or just "id" based on output.title-length config.
func formatFeedbackID(id, title string) string {
	title = applyTitleConfig(title)
	if title == "" {
		return id
	}
	return id + " — " + title
}

// formatFeedbackIDParen returns "id (title)" for multi-ID messages (dep commands).
func formatFeedbackIDParen(id, title string) string {
	title = applyTitleConfig(title)
	if title == "" {
		return id
	}
	return id + " (" + title + ")"
}

// applyTitleConfig applies the output.title-length config to a title string.
// Returns empty string when titles should be hidden (<= 0).
//
// The title is sanitized for terminal display first (beads-j8li): a title can
// originate from an untrusted external source (SCM/JSONL/markdown import, which
// store the raw title without sanitizing) and is echoed here on local feedback
// lines (create/close/comment/dep via formatFeedbackID). SanitizeForTerminal
// strips CSI/OSC/C0/C1 escapes — notably OSC 52 (clipboard) / OSC 0 (title) —
// so a malicious imported title can't inject terminal escapes when redisplayed.
// This is display-only: the STORED title is untouched, preserving SCM
// round-trip fidelity. applyTitleConfig is the single chokepoint both
// formatFeedbackID and formatFeedbackIDParen route through.
func applyTitleConfig(title string) string {
	if title == "" {
		return ""
	}
	title = ui.SanitizeForTerminal(title)
	maxLen := config.GetInt("output.title-length")
	switch {
	case maxLen <= 0:
		return "" // hide titles
	default:
		return truncateTitle(title, maxLen)
	}
}

// issueTitleOrEmpty returns the title of an issue, or empty string if issue is nil.
func issueTitleOrEmpty(issue *types.Issue) string {
	if issue == nil {
		return ""
	}
	return issue.Title
}

// lookupTitle returns the title for an issue ID, or empty string on failure.
// Best-effort lookup for feedback messages — never fails the command.
func lookupTitle(id string) string {
	if store == nil || IsExternalRef(id) {
		return ""
	}
	issue, err := store.GetIssue(rootCtx, id)
	if err != nil || issue == nil {
		return ""
	}
	return issue.Title
}
