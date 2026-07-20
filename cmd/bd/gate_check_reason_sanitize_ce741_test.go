package main

import (
	"fmt"
	"strings"
	"testing"
)

// beads-ce741 (7n9y sink class): `bd gate check` printed the per-gate reason
// RAW at 5 sites in the check loop. For gh:pr / gh:run gates the reason embeds
// UNTRUSTED external SCM data — the GitHub PR title (checkGHPR, `gh pr view
// --json state,title`) or workflow name (checkGHRunStatus, `gh run view --json
// ...,name`) — so a PR title carrying OSC/CSI escapes injected terminal-control
// sequences into the maintainer's terminal. Fix routes the DISPLAY through
// formatGateCheckReason (ui.SanitizeForTerminal); the raw reason still flows to
// closeGate/escalateGate for stored/relayed fidelity.

func TestFormatGateCheckReason_sanitize_ce741(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	t.Run("strips escapes from a gh:pr reason", func(t *testing.T) {
		// Simulate checkGHPR's reason with an attacker-controlled PR title.
		reason := fmt.Sprintf("PR '%s' was merged", "Fix"+osc+csi+"bug")
		got := formatGateCheckReason(reason)
		if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
			t.Errorf("formatGateCheckReason leaked a raw escape: %q", got)
		}
		if !strings.Contains(got, "PR 'Fixbug' was merged") {
			t.Errorf("formatGateCheckReason dropped visible text: %q", got)
		}
	})

	t.Run("preserves a clean reason unchanged", func(t *testing.T) {
		reason := "workflow 'ci build' succeeded"
		if got := formatGateCheckReason(reason); got != reason {
			t.Errorf("formatGateCheckReason mutated a clean reason: got %q want %q", got, reason)
		}
	})
}
