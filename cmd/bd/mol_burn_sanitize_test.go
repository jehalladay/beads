package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-7n9y: the `bd mol burn --dry-run` tree printed wisp/molecule
// issue.Title RAW via fmt.Printf (mol_burn.go), bypassing the
// ui.SanitizeForTerminal sanitize that `bd show` applies. A title can
// originate from an untrusted import (JSONL/markdown/SCM) carrying OSC/CSI
// terminal-control escapes (OSC 0 window-title / OSC 52 clipboard), so the
// dry-run render injected control sequences onto these lines. The fix routes
// every Title sink through displayTitle. Sink-class tail of j8li/ihaw.
func TestPrintBurnDryRunIssues_sanitize_7n9y(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	root := &types.Issue{
		ID:        "bd-root",
		Title:     "Root Title" + osc,
		Status:    types.StatusOpen,
		Ephemeral: true,
	}
	child := &types.Issue{
		ID:        "bd-child",
		Title:     "Child" + csi + osc + "Title",
		Status:    types.StatusOpen,
		Ephemeral: true,
	}
	sg := &TemplateSubgraph{
		Root:   root,
		Issues: []*types.Issue{root, child},
	}

	assertClean := func(t *testing.T, label, got string) {
		t.Helper()
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (\\x1b): %q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (\\x07): %q", label, got)
		}
		// Visible text must survive.
		if !strings.Contains(got, "Root Title") || !strings.Contains(got, "ChildTitle") {
			t.Errorf("%s dropped visible title text: %q", label, got)
		}
		// The ROOT marker and IDs must still render.
		if !strings.Contains(got, "[ROOT]") || !strings.Contains(got, "bd-root") || !strings.Contains(got, "bd-child") {
			t.Errorf("%s dropped structural output: %q", label, got)
		}
	}

	t.Run("wisp path (ephemeralOnly)", func(t *testing.T) {
		out := captureStdout(t, func() error {
			printBurnDryRunIssues(sg, true)
			return nil
		})
		assertClean(t, "printBurnDryRunIssues(ephemeralOnly=true)", out)
	})

	t.Run("persistent mol path", func(t *testing.T) {
		out := captureStdout(t, func() error {
			printBurnDryRunIssues(sg, false)
			return nil
		})
		assertClean(t, "printBurnDryRunIssues(ephemeralOnly=false)", out)
	})

	t.Run("ephemeralOnly skips non-ephemeral issues", func(t *testing.T) {
		persistent := &types.Issue{ID: "bd-persist", Title: "Persistent", Status: types.StatusOpen}
		sg2 := &TemplateSubgraph{Root: root, Issues: []*types.Issue{root, persistent}}
		out := captureStdout(t, func() error {
			printBurnDryRunIssues(sg2, true)
			return nil
		})
		if strings.Contains(out, "bd-persist") {
			t.Errorf("ephemeralOnly=true should skip non-ephemeral issue, got: %q", out)
		}
	})
}
