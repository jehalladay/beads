package main

import (
	"strings"
	"testing"
)

// TestPrintSwarmCreateSummary_SanitizesTitle_rbmia is the sanitize teeth for
// beads-rbmia (7n9y direct-twin sink slice). The DIRECT 'bd swarm create'
// confirmation printed the epic title RAW at swarm.go:1079
// (`fmt.Printf("   Epic: %s (%s)\n", epicID, epicTitle)`), bypassing
// ui.SanitizeForTerminal. epicTitle derives from a stored issue title
// (epic.Title / "Swarm Epic: "+issue.Title), which can originate from an
// untrusted import (JSONL/markdown/SCM) carrying OSC/CSI escapes (OSC 0
// window-title / OSC 52 clipboard). gqn5v covered analyze/status and ry48z
// covered the proxied create-summary twin; this closes the direct create path
// by delegating to the shared printSwarmCreateSummary (which routes epicTitle
// through displayTitle). Display-only: the --json path is unchanged.
func TestPrintSwarmCreateSummary_SanitizesTitle_rbmia(t *testing.T) {
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
		printSwarmCreateSummary("bd-swarm", "bd-epic", "Epic"+csi+osc+"Title", "coord", analysis)
		return nil
	})
	assertNoRawEscapes(t, out, "swarm create direct summary")
	if !strings.Contains(out, "EpicTitle") {
		t.Errorf("swarm create summary dropped/garbled epic title (rbmia): %q", out)
	}
}
