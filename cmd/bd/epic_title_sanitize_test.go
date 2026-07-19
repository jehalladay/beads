package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEpicTitleSanitize_1wrub is the sanitize teeth for beads-1wrub (7n9y
// sink-class slice). `bd epic status` (renderEpicStatus, epic.go:98) printed
// ui.RenderBold(epic.Title) and `bd epic close-eligible --dry-run`
// (renderEpicCloseEligible, epic.go:175) printed epicStatus.Epic.Title RAW —
// neither lipgloss Render nor the bare Printf strips terminal escapes. Epic
// titles are store-read (an imported epic carries its title verbatim), so an
// OSC/CSI sequence reached the terminal. The fix routes each through
// displayTitle (ui.SanitizeForTerminal); display-only — the --json path and
// the stored title stay raw.
//
// Both render funcs are pure (take []*types.EpicStatus, write stdout), so this
// exercises them directly via captureStdout with jsonOutput=false.
func epicWithEscapeTitle() []*types.EpicStatus {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	return []*types.EpicStatus{
		{
			Epic: &types.Issue{
				ID:    "beads-evil",
				Title: "Danger" + csi + osc + "Title",
			},
			TotalChildren:    2,
			ClosedChildren:   2,
			EligibleForClose: true,
		},
	}
}

func assertNoEscapesTitleKept_1wrub(t *testing.T, out, label string) {
	t.Helper()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("%s leaked a raw ESC (\\x1b) — epic.Title not sanitized (beads-1wrub):\n%q", label, out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("%s leaked a raw BEL (\\x07) — epic.Title not sanitized (beads-1wrub):\n%q", label, out)
	}
	if !strings.Contains(out, "Danger") || !strings.Contains(out, "Title") {
		t.Errorf("%s dropped the visible title text (over-sanitized): %q", label, out)
	}
}

func TestEpicStatus_SanitizesTitle_1wrub(t *testing.T) {
	old := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = old }()

	out := captureStdout(t, func() error {
		return renderEpicStatus(epicWithEscapeTitle(), false)
	})
	assertNoEscapesTitleKept_1wrub(t, out, "bd epic status")
}

func TestEpicCloseEligibleDryRun_SanitizesTitle_1wrub(t *testing.T) {
	old := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = old }()

	out := captureStdout(t, func() error {
		// dryRun=true → preview path (epic.go:175); closeFn/commitFn unused.
		return renderEpicCloseEligible(epicWithEscapeTitle(), true, nil, nil)
	})
	assertNoEscapesTitleKept_1wrub(t, out, "bd epic close-eligible --dry-run")
}
