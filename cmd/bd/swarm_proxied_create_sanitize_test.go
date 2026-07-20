package main

import (
	"strings"
	"testing"
)

// TestPrintSwarmCreateProxiedSummary_SanitizesTitle_ry48z is the sanitize teeth
// for beads-ry48z (7n9y proxied-twin sink slice). runSwarmCreateProxied's human
// confirmation printed the epic title RAW via bare fmt.Printf
// (swarm_proxied_server.go:360, "Epic: %s (%s)"), bypassing
// ui.SanitizeForTerminal. epicTitle derives from a stored issue title
// (epic.Title), which can originate from an untrusted import (JSONL/markdown/SCM)
// carrying OSC/CSI escapes (OSC 0 window-title / OSC 52 clipboard). The fix
// extracts the print block into printSwarmCreateProxiedSummary and routes
// epicTitle through displayTitle; display-only (the --json path is unchanged).
func TestPrintSwarmCreateProxiedSummary_SanitizesTitle_ry48z(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	analysis := &SwarmAnalysis{
		EpicID:         "bd-epic",
		TotalIssues:    3,
		MaxParallelism: 2,
		ReadyFronts: []ReadyFront{
			{Wave: 0, Issues: []string{"bd-1"}},
		},
	}
	out := captureStdout(t, func() error {
		printSwarmCreateProxiedSummary("bd-swarm", "bd-epic", "Epic"+csi+osc+"Title", "coord", analysis)
		return nil
	})
	assertNoRawEscapes(t, out, "swarm create proxied summary")
	if !strings.Contains(out, "EpicTitle") {
		t.Errorf("swarm create proxied summary dropped/garbled epic title (L360): %q", out)
	}
}
