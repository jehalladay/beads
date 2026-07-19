package main

import (
	"strings"
	"testing"
)

// TestRenderSwarm_SanitizesTitle_gqn5v is the sanitize teeth for beads-gqn5v
// (7n9y sink-class slice). The human swarm views printed titles RAW via bare
// fmt.Printf, bypassing ui.SanitizeForTerminal:
//   - renderSwarmAnalysis: the epic title (swarm.go:523, analysis.EpicTitle)
//     and each ready-front wave issue title (swarm.go:542, front.Titles[i]);
//   - renderSwarmStatus: the epic title (swarm.go:808, status.EpicTitle).
//
// EpicTitle/Titles derive from stored issue titles (epic.Title), which can
// originate from an untrusted import (JSONL/markdown/SCM) carrying OSC/CSI
// escapes (OSC 0 window-title / OSC 52 clipboard). The fix routes all three
// through displayTitle; display-only (stored titles + the --json path are
// unchanged). Both render funcs are pure, so this calls them via captureStdout.
func TestRenderSwarm_SanitizesTitle_gqn5v(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	t.Run("analyze", func(t *testing.T) {
		analysis := &SwarmAnalysis{
			EpicID:      "bd-epic",
			EpicTitle:   "EpicAnalyze" + csi + osc + "Title",
			TotalIssues: 2,
			ReadyFronts: []ReadyFront{
				{Wave: 0, Issues: []string{"bd-1"}, Titles: []string{"Wave" + osc + "Task"}},
			},
		}
		out := captureStdout(t, func() error {
			renderSwarmAnalysis(analysis)
			return nil
		})
		assertNoRawEscapes(t, out, "swarm analyze")
		if !strings.Contains(out, "EpicAnalyzeTitle") {
			t.Errorf("swarm analyze dropped/garbled epic title (L523): %q", out)
		}
		if !strings.Contains(out, "WaveTask") {
			t.Errorf("swarm analyze dropped/garbled wave title (L542): %q", out)
		}
	})

	t.Run("status", func(t *testing.T) {
		status := &SwarmStatus{
			EpicID:    "bd-epic",
			EpicTitle: "EpicStatus" + csi + osc + "Title",
		}
		out := captureStdout(t, func() error {
			renderSwarmStatus(status)
			return nil
		})
		assertNoRawEscapes(t, out, "swarm status")
		if !strings.Contains(out, "EpicStatusTitle") {
			t.Errorf("swarm status dropped/garbled epic title (L808): %q", out)
		}
	})
}
