package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-2ktwm (7n9y slice): the proxied-server dep views printed issue titles
// RAW via fmt.Printf, bypassing ui.SanitizeForTerminal — the proxied twins of
// the direct dep.go sinks (dep.go:1017/1351). runDepListProxiedServer's list
// line and runDepCyclesProxiedServer's cycle list both echoed iss.Title /
// issue.Title unsanitized. A title can originate from an untrusted import
// (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard), so these views injected control sequences
// onto their lines. The fix routes both through displayTitle. Display-only —
// stored titles and the JSON path are unchanged.

func TestPrintProxiedDepList_sanitize(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	issues := []*types.IssueWithDependencyMetadata{
		{
			Issue: types.Issue{
				ID:       "bd-a",
				Title:    "Alpha" + osc + "Dep",
				Priority: 2,
				Status:   types.StatusOpen,
			},
			DependencyType: types.DependencyType("blocks"),
		},
		{
			Issue: types.Issue{
				ID:       "bd-b",
				Title:    "Beta" + csi + osc + "Dep",
				Priority: 1,
				Status:   types.StatusClosed,
			},
			DependencyType: types.DependencyType("related"),
		},
	}

	out := captureStdout(t, func() error {
		printProxiedDepList(issues)
		return nil
	})

	assertNoRawEscapes(t, out, "proxied dep list")
	for _, want := range []string{"AlphaDep", "BetaDep", "bd-a", "bd-b"} {
		if !strings.Contains(out, want) {
			t.Errorf("proxied dep list dropped %q: %q", want, out)
		}
	}
}

func TestPrintProxiedDepCycles_sanitize(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	cycles := [][]*types.Issue{
		{
			{ID: "bd-x", Title: "Gamma" + osc + "Cyc"},
			{ID: "bd-y", Title: "Delta" + csi + osc + "Cyc"},
		},
	}

	out := captureStdout(t, func() error {
		printProxiedDepCycles(cycles)
		return nil
	})

	assertNoRawEscapes(t, out, "proxied dep cycles")
	for _, want := range []string{"GammaCyc", "DeltaCyc", "bd-x", "bd-y"} {
		if !strings.Contains(out, want) {
			t.Errorf("proxied dep cycles dropped %q: %q", want, out)
		}
	}
}

func assertNoRawEscapes(t *testing.T, out, label string) {
	t.Helper()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("%s leaked a raw ESC (\\x1b): %q", label, out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("%s leaked a raw BEL (\\x07): %q", label, out)
	}
}
